//go:build dbtest

package readapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	formsrepositories "github.com/materm-organization/materm-web-portal-backend/modules/forms/repositories"
	formsservices "github.com/materm-organization/materm-web-portal-backend/modules/forms/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/iphash"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/portalapp"
)

const submissions = "/api/v1/public/forms/contact/submissions"

var ipCounter int

// nextIP returns an address no other test used (TEST-NET-2, RFC 5737).
func nextIP() string {
	ipCounter++
	return "198.51.100." + strconv.Itoa(ipCounter%250+1)
}

func marker(t *testing.T) string {
	return fmt.Sprintf("Marker %s %d hello world", strings.ReplaceAll(t.Name(), "/", "-"), time.Now().UnixNano())
}

// body builds a P-15 body for the seeded contact form; mut may change the top-level members or the data.
func body(msg string, mut ...func(m obj)) []byte {
	m := obj{
		"locale": "en", "source_path": "/contact", "honeypot": "", "elapsed_ms": 8200, "consent": nil,
		"data": obj{"name": "Noy Phommasan", "email": "noy@example.com", "phone": "", "topic": "booking", "message": msg},
	}
	for _, f := range mut {
		f(m)
	}
	b, _ := json.Marshal(m)
	return b
}

func data(m obj) obj { return m["data"].(obj) }

func submit(t *testing.T, e *env, path string, b []byte, ip string) portalapp.Response {
	t.Helper()
	return e.app.Do(t, portalapp.Request{Method: "POST", Path: path, Body: b, Header: map[string]string{
		"Content-Type": "application/json", "X-Client-IP": ip, "X-Client-User-Agent": "UA-test/1.0", "X-Request-Id": "test-request",
	}})
}

type row struct {
	Status    string
	Spam      int
	IPHash    []byte
	UserAgent sql.NullString
	Locale    sql.NullString
	Source    sql.NullString
	Data      string
	Consent   sql.NullString
	AsText    string
}

// rowsFor reads the stored submissions that carry marker, through the OWNER connection (the portal role cannot
// read the table).
func rowsFor(t *testing.T, e *env, marker string) []row {
	t.Helper()
	rs, err := e.s.Owner.QueryContext(context.Background(), `
	  select status, spam_score, ip_hash, user_agent, locale_code, source_path, data::text, consent_version, to_jsonb(s)::text
	  from forms_form_submissions s where data::text like '%' || $1 || '%' order by id`, marker)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.Status, &r.Spam, &r.IPHash, &r.UserAgent, &r.Locale, &r.Source, &r.Data, &r.Consent, &r.AsText); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func received(t *testing.T, r portalapp.Response) {
	t.Helper()
	if r.Status != 200 || string(r.Body) != `{"success":true,"data":{"status":"received"}}` {
		t.Fatalf("expected 200 received, got %d %s", r.Status, r.Body)
	}
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control %q", r.Header.Get("Cache-Control"))
	}
}

// ---- P-14 ---------------------------------------------------------------------------------------------------------

func TestFormDefinitionMatchesTheFrontendBundle(t *testing.T) {
	e := wire(t)
	r := e.app.Get(t, "/api/v1/public/forms/contact?locale=en", nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	got := decode(t, r.Body)["data"].(obj)

	// the bundled seed of the frontend is the contract: same members, same values (min_fill_seconds is additive)
	raw, err := os.ReadFile("../../../materm-web-portal-frontend/src/content/cms-seed/data/en/forms/contact.json")
	if err != nil {
		t.Skipf("frontend bundle not present: %v", err)
	}
	want := decode(t, raw)["data"].(obj)
	if got["min_fill_seconds"] != float64(3) {
		t.Errorf("min_fill_seconds = %v", got["min_fill_seconds"])
	}
	delete(got, "min_fill_seconds")
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if !bytes.Equal(gb, wb) {
		t.Errorf("the definition differs from the frontend bundle\n got: %s\nwant: %s", gb, wb)
	}
	// nothing that belongs to the server
	for _, banned := range []string{"rate_limit", "max_payload", "recipient", "retention", "x-pii", "notify", "store_submissions"} {
		if strings.Contains(string(r.Body), banned) {
			t.Errorf("the definition mentions %q", banned)
		}
	}
	// the tags
	var fid int64
	ownerScalar(t, e.s, &fid, `select id from forms_forms where slug = 'contact'`)
	if len(members(t, e, fmt.Sprintf("form:%d", fid))) != 1 {
		t.Error("form:<id> not emitted")
	}
	// Lao falls back per field and keeps the field order
	la := decode(t, e.app.Get(t, "/api/v1/public/forms/contact?locale=la", nil).Body)
	if at(la, "data.resolved_locale") != "la" || length(at(la, "data.fields")) != 5 || at(la, "data.fields.0.key") != "name" {
		t.Errorf("la: %s", e.app.Get(t, "/api/v1/public/forms/contact?locale=la", nil).Body)
	}
}

func TestFormDefinitionHidesUnpublishedForms(t *testing.T) {
	e := wire(t)
	ownerExec(t, e.s, limitForm)
	t.Cleanup(func() { dropLimitForm(t, e) })
	if r := e.app.Get(t, "/api/v1/public/forms/lim?locale=en", nil); r.Status != 200 {
		t.Fatalf("published: %d", r.Status)
	}
	ownerExec(t, e.s, `update forms_forms set status = 'draft' where slug = 'lim'`)
	e2 := wire(t) // a fresh cache
	for _, slug := range []string{"lim", "nope", "Bad_Slug", "1x"} {
		r := e2.app.Get(t, "/api/v1/public/forms/"+slug+"?locale=en", nil)
		if r.Status != 404 || r.Header.Get("Cache-Control") != "public, max-age=30" {
			t.Errorf("%s: %d %q", slug, r.Status, r.Header.Get("Cache-Control"))
		}
	}
	// and nothing can be submitted to it
	if r := submit(t, e2, "/api/v1/public/forms/lim/submissions", []byte(`{"locale":"en","data":{"name":"Abc"},"elapsed_ms":9000}`), nextIP()); r.Status != 404 {
		t.Errorf("submit to an unpublished form: %d %s", r.Status, r.Body)
	}
}

// ---- P-15 ---------------------------------------------------------------------------------------------------------

const limitForm = `
insert into forms_forms (slug, name, json_schema, field_order, honeypot_field, rate_limit_max, rate_limit_window_s, rate_limit_daily_max,
                         min_fill_seconds, max_payload_bytes, status, published_at)
values ('lim', 'Limits', '{"type":"object","additionalProperties":false,"required":["name"],"properties":{"name":{"type":"string","minLength":3,"maxLength":50}}}'::jsonb,
        array['name'], 'hp', 3, 600, 2, 0, 256, 'published', timestamptz '2026-01-01 00:00+00');
insert into forms_form_translations (form_id, locale_code, title, submit_label)
select f.id, l.code, 'Limits', 'Go' from forms_forms f cross join (values ('la'), ('en')) l(code) where f.slug = 'lim';
insert into forms_form_field_translations (form_id, field_key, locale_code, label)
select f.id, 'name', l.code, 'Name' from forms_forms f cross join (values ('la'), ('en')) l(code) where f.slug = 'lim';`

func dropLimitForm(t *testing.T, e *env) {
	ownerExec(t, e.s, `
	  delete from forms_form_submissions where form_id in (select id from forms_forms where slug = 'lim');
	  delete from forms_forms where slug = 'lim';`)
}

func TestSubmitStoresARowWithTheSaltedHashAndNoAddress(t *testing.T) {
	e := wire(t)
	msg, ip := marker(t), nextIP()
	received(t, submit(t, e, submissions, body(msg), ip))

	rs := rowsFor(t, e, msg)
	if len(rs) != 1 {
		t.Fatalf("%d rows", len(rs))
	}
	r := rs[0]
	if r.Status != "new" || r.Spam != 0 || r.Locale.String != "en" || r.Source.String != "/contact" || r.UserAgent.String != "UA-test/1.0" || r.Consent.Valid {
		t.Errorf("row: %+v", r)
	}
	var stored obj
	if err := json.Unmarshal([]byte(r.Data), &stored); err != nil {
		t.Fatal(err)
	}
	if stored["name"] != "Noy Phommasan" || stored["email"] != "noy@example.com" || stored["topic"] != "booking" || stored["message"] != msg || stored["phone"] != "" || len(stored) != 5 {
		t.Errorf("data: %s", r.Data)
	}
	// ip_hash is HMAC-SHA256(ip, today's salt) - recompute it from the salt in Redis
	salt, err := e.rds.Client.Get(context.Background(), e.keys.IPSalt(time.Now())).Bytes()
	if err != nil || len(salt) != 32 {
		t.Fatalf("salt: %d bytes, %v", len(salt), err)
	}
	if want := iphash.Compute(ip, salt); !bytes.Equal(r.IPHash, want.Bytes()) {
		t.Errorf("ip_hash is not the daily salted HMAC of the address")
	}
	if ttl := e.rds.Client.TTL(context.Background(), e.keys.IPSalt(time.Now())).Val(); ttl <= 24*time.Hour || ttl > 48*time.Hour {
		t.Errorf("salt TTL %s, want <= 48h", ttl)
	}
	// the address is nowhere: not in the row (any column), not in a Redis key or a cached value
	if strings.Contains(r.AsText, ip) || strings.Contains(r.AsText, "198.51.100") {
		t.Fatalf("the raw address is in the stored row: %s", r.AsText)
	}
	keys, err := e.rds.Client.Keys(context.Background(), e.rds.Prefix+"*").Result()
	if err != nil || len(keys) == 0 {
		t.Fatal(err, keys)
	}
	for _, k := range keys {
		if strings.Contains(k, ip) || strings.Contains(k, "198.51.100") {
			t.Errorf("address in a Redis key: %s", k)
		}
		if v, err := e.rds.Client.Get(context.Background(), k).Result(); err == nil && (strings.Contains(v, msg) || strings.Contains(v, ip)) {
			t.Errorf("personal data in the value of %s", k)
		}
	}
	// the one write path is the only thing that is not cached: nothing about the submission is under c:
	for _, k := range keys {
		if strings.Contains(k, ":c:") && strings.Contains(k, "submission") {
			t.Errorf("a submission response was cached: %s", k)
		}
	}
}

func TestSameVisitorSameDaySameHashOthersDiffer(t *testing.T) {
	e := wire(t)
	m1, m2, m3 := marker(t)+" one", marker(t)+" two", marker(t)+" three"
	ipA, ipB := nextIP(), nextIP()
	received(t, submit(t, e, submissions, body(m1), ipA))
	received(t, submit(t, e, submissions, body(m2), ipA))
	received(t, submit(t, e, submissions, body(m3), ipB))
	h := func(m string) []byte { return rowsFor(t, e, m)[0].IPHash }
	if !bytes.Equal(h(m1), h(m2)) {
		t.Error("one visitor, one day: the hash must be the same (it is what the rate limit counts)")
	}
	if bytes.Equal(h(m1), h(m3)) {
		t.Error("two visitors share a hash")
	}
	// an IPv6 visitor is identified by its /64
	m4, m5 := marker(t)+" four", marker(t)+" five"
	received(t, submit(t, e, submissions, body(m4), "2001:db8:1:2:aaaa:bbbb:cccc:1111"))
	received(t, submit(t, e, submissions, body(m5), "2001:db8:1:2:1111:2222:3333:4444"))
	if !bytes.Equal(h(m4), h(m5)) {
		t.Error("two addresses of one /64 must count as one visitor")
	}
}

func TestSpamIsStoredAsSpamAndAnsweredLikeAnythingElse(t *testing.T) {
	e := wire(t)
	normal := submit(t, e, submissions, body(marker(t)+" ok"), nextIP())
	received(t, normal)

	cases := []struct {
		name  string
		mut   func(m obj)
		score int
	}{
		{"honeypot", func(m obj) { m["honeypot"] = "http://spam.example" }, 100},
		{"honeypot posted as a data member", func(m obj) { data(m)["company"] = "ACME" }, 100},
		{"too fast", func(m obj) { m["elapsed_ms"] = 900 }, 90},
		{"no elapsed_ms", func(m obj) { delete(m, "elapsed_ms") }, 90},
		{"stamp from the future", func(m obj) { m["rendered_at"] = time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339) }, 90},
		{"stamp within the clock skew", func(m obj) { m["rendered_at"] = time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339) }, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := marker(t)
			r := submit(t, e, submissions, body(msg, c.mut), nextIP())
			received(t, r)
			// not a byte of difference to a normal submission that a bot could use as a signal (the request id is per request)
			if string(r.Body) != string(normal.Body) || r.Status != normal.Status {
				t.Errorf("answer differs from the normal one: %s", r.Body)
			}
			for _, h := range []string{"Content-Type", "Cache-Control", "Content-Language", "Retry-After", "ETag"} {
				if r.Header.Get(h) != normal.Header.Get(h) {
					t.Errorf("header %s differs: %q vs %q", h, r.Header.Get(h), normal.Header.Get(h))
				}
			}
			rs := rowsFor(t, e, msg)
			wantStatus := "spam"
			if c.score == 0 {
				wantStatus = "new"
			}
			if len(rs) != 1 || rs[0].Spam != c.score || rs[0].Status != wantStatus {
				t.Fatalf("stored %+v, want status %s score %d", rs, wantStatus, c.score)
			}
			if strings.Contains(rs[0].Data, "company") {
				t.Errorf("the honeypot member was stored: %s", rs[0].Data)
			}
		})
	}
}

func TestInvalidSubmissionIs422AndNothingIsStored(t *testing.T) {
	e := wire(t)
	msg := "short"
	b := body(msg, func(m obj) {
		data(m)["name"], data(m)["email"], data(m)["topic"], data(m)["phone"] = "x", "not-an-email", "refund", "abc"
		data(m)["extra"] = "SECRET-EXTRA-VALUE"
	})
	r := submit(t, e, submissions, b, nextIP())
	if r.Status != 422 || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	got := map[string]string{}
	for _, fe := range decode(t, r.Body)["errors"].([]any) {
		f := fe.(obj)
		if f["in"] != "body" {
			t.Errorf("location %v", f)
		}
		got[str(f["path"])] = str(f["code"])
	}
	want := map[string]string{"/data/name": "required", "/data/email": "email", "/data/topic": "required", "/data/phone": "phone", "/data/message": "messageShort", "/data/extra": "unknown_field"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	if strings.Contains(string(r.Body), "SECRET-EXTRA-VALUE") || strings.Contains(string(r.Body), "not-an-email") {
		t.Errorf("a submitted value is echoed: %s", r.Body)
	}
	var n int
	ownerScalar(t, e.s, &n, `select count(*) from forms_form_submissions where data::text like '%SECRET-EXTRA-VALUE%'`)
	if n != 0 {
		t.Errorf("%d rows for an invalid submission", n)
	}
	// malformed requests
	for name, c := range map[string]struct {
		body   string
		status int
	}{"bad json": {`{"locale":`, 400}, "unknown member": {`{"locale":"en","evil":1}`, 422}, "bad locale": {`{"locale":"x y"}`, 422}} {
		if r := submit(t, e, submissions, []byte(c.body), nextIP()); r.Status != c.status {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if r := e.app.Do(t, portalapp.Request{Method: "POST", Path: submissions, Body: []byte(`{}`), Header: map[string]string{"Content-Type": "text/plain"}}); r.Status != 415 {
		t.Errorf("text/plain: %d", r.Status)
	}
	if r := e.app.Do(t, portalapp.Request{Method: "POST", Path: submissions, Body: []byte(`{}`), NoKey: true, Header: map[string]string{"Content-Type": "application/json"}}); r.Status != 401 {
		t.Errorf("no portal key: %d", r.Status)
	}
}

func TestSubmitSanitisesLikeTheFrontend(t *testing.T) {
	e := wire(t)
	msg := marker(t)
	received(t, submit(t, e, submissions, body(msg, func(m obj) {
		data(m)["name"] = "  Noy\t\n  Phommasan\u0000 "
		data(m)["message"] = msg + "\r\nsecond\x07 line"
	}), nextIP()))
	var stored obj
	if err := json.Unmarshal([]byte(rowsFor(t, e, msg)[0].Data), &stored); err != nil {
		t.Fatal(err)
	}
	if stored["name"] != "Noy Phommasan" || stored["message"] != msg+"\nsecond line" {
		t.Errorf("stored %v", stored)
	}
}

func TestPerVisitorWindowLimitAnd429(t *testing.T) {
	e := wire(t)
	ip := nextIP()
	var msgs []string
	for i := 1; i <= 5; i++ {
		m := fmt.Sprintf("%s number %d", marker(t), i)
		msgs = append(msgs, m)
		received(t, submit(t, e, submissions, body(m), ip))
	}
	m6 := marker(t) + " sixth"
	r := submit(t, e, submissions, body(m6), ip)
	b := decode(t, r.Body)
	if r.Status != 429 || b["code"] != "RATE_LIMITED" || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	ra, err := strconv.Atoi(r.Header.Get("Retry-After"))
	if err != nil || ra < 1 || ra > 600 {
		t.Errorf("Retry-After %q, want 1..600", r.Header.Get("Retry-After"))
	}
	if len(rowsFor(t, e, m6)) != 0 {
		t.Error("the refused submission was stored")
	}
	for _, m := range msgs {
		if len(rowsFor(t, e, m)) != 1 {
			t.Errorf("%q: not stored", m)
		}
	}
	// the window counter name carries the form, the hashed visitor and the window start - never the address
	var found bool
	for _, k := range e.rds.Client.Keys(context.Background(), e.rds.Prefix+"rl:form:contact:*:w:*").Val() {
		found = true
		if strings.Contains(k, ip) {
			t.Errorf("address in key %s", k)
		}
	}
	if !found {
		t.Error("no window counter in Redis")
	}
	// another visitor is not affected
	received(t, submit(t, e, submissions, body(marker(t)+" other"), nextIP()))
}

func TestDailyLimitFromTheFormAndPayloadLimit(t *testing.T) {
	e := wire(t)
	ownerExec(t, e.s, limitForm)
	t.Cleanup(func() { dropLimitForm(t, e) })
	path := "/api/v1/public/forms/lim/submissions"
	ip := nextIP()
	mk := func(name string) []byte {
		return []byte(fmt.Sprintf(`{"locale":"en","data":{"name":%q},"elapsed_ms":0}`, name))
	}
	received(t, submit(t, e, path, mk("First one"), ip))
	received(t, submit(t, e, path, mk("Second one"), ip))
	r := submit(t, e, path, mk("Third one"), ip)
	if r.Status != 429 {
		t.Fatalf("the form allows 2 per day: %d %s", r.Status, r.Body)
	}
	// the daily wait ends at midnight UTC (not after the 25 h cleanup TTL of the key)
	ra, _ := strconv.Atoi(r.Header.Get("Retry-After"))
	toMidnight := int(time.Until(time.Now().UTC().Truncate(24*time.Hour).Add(24*time.Hour)) / time.Second)
	if ra < 1 || ra > toMidnight+2 {
		t.Errorf("Retry-After %d, midnight is %d s away", ra, toMidnight)
	}
	// min_fill_seconds = 0 on this form: elapsed 0 is not "too fast"
	var spam int
	ownerScalar(t, e.s, &spam, `select spam_score from forms_form_submissions where data->>'name' = 'First one'`)
	if spam != 0 {
		t.Errorf("min_fill_seconds=0 flagged: %d", spam)
	}
	// max_payload_bytes + 2 KB on the raw body: 413
	big := []byte(`{"locale":"en","data":{"name":"Abc"},"elapsed_ms":0,"source_path":"` + strings.Repeat("a", 2400) + `"}`)
	if r := submit(t, e, path, big, nextIP()); r.Status != 413 || decode(t, r.Body)["code"] != "PAYLOAD_TOO_LARGE" {
		t.Errorf("oversize body: %d %s", r.Status, r.Body)
	}
}

func TestAggregateCapPerPortalKey(t *testing.T) {
	e := wire(t, wireOpts{Forms: formsservices.Options{KeyPerMinute: 3}})
	var ok, limited int
	for i := 0; i < 6; i++ { // a "different visitor" every time: the per-visitor limits see nothing
		r := submit(t, e, submissions, body(fmt.Sprintf("%s flood %d", marker(t), i)), fmt.Sprintf("203.0.113.%d", 10+i))
		switch r.Status {
		case 200:
			ok++
		case 429:
			limited++
		default:
			t.Fatalf("%d %s", r.Status, r.Body)
		}
	}
	if ok != 3 || limited != 3 {
		t.Fatalf("ok %d limited %d, want 3 and 3", ok, limited)
	}
	if len(e.rds.Client.Keys(context.Background(), e.rds.Prefix+"rl:form-key:*").Val()) == 0 {
		t.Error("no aggregate counter in Redis")
	}
}

func TestIdenticalRetryStoresOneRow(t *testing.T) {
	e := wire(t)
	msg, ip := marker(t), nextIP()
	for i := 0; i < 3; i++ {
		received(t, submit(t, e, submissions, body(msg), ip))
	}
	if n := len(rowsFor(t, e, msg)); n != 1 {
		t.Fatalf("%d rows for 3 identical submissions", n)
	}
	// someone else sending the same words is a different submission
	received(t, submit(t, e, submissions, body(msg), nextIP()))
	if n := len(rowsFor(t, e, msg)); n != 2 {
		t.Fatalf("%d rows", n)
	}
}

func TestRedisDownRefusesSubmissionsAndStoresNothing(t *testing.T) {
	e := wire(t, wireOpts{DeadRedis: true})
	msg := marker(t)
	r := submit(t, e, submissions, body(msg), nextIP())
	b := decode(t, r.Body)
	if r.Status != http.StatusServiceUnavailable || b["code"] != "SERVICE_UNAVAILABLE" || r.Header.Get("Cache-Control") != "no-store" || r.Header.Get("Retry-After") == "" {
		t.Fatalf("%d %s %v", r.Status, r.Body, r.Header)
	}
	if n := len(rowsFor(t, e, msg)); n != 0 {
		t.Fatalf("%d rows stored while the limiter was down", n)
	}
	// the answer does not name the dependency
	if strings.Contains(strings.ToLower(string(r.Body)), "redis") {
		t.Errorf("the 503 names Redis: %s", r.Body)
	}
}

func TestPortalRoleCannotReadOrWriteSubmissionsButCanCallTheFunction(t *testing.T) {
	e := wire(t)
	ctx := context.Background()
	state := func(err error) string {
		var pe *pgconn.PgError
		if errors.As(err, &pe) {
			return pe.Code
		}
		return fmt.Sprint(err)
	}
	var n int
	if err := e.s.RO.QueryRowContext(ctx, `select count(*) from forms_form_submissions`).Scan(&n); state(err) != "42501" {
		t.Errorf("select on the inbox: %v", err)
	}
	if _, err := e.s.RO.ExecContext(ctx, `insert into forms_form_submissions (form_id, data, retention_until) values (1, '{}', now())`); state(err) != "42501" {
		t.Errorf("insert into the inbox: %v", err)
	}
	if _, err := e.s.RO.ExecContext(ctx, `insert into ops_outbox (event_type, aggregate_type) values ('x', 'y')`); state(err) != "42501" {
		t.Errorf("insert into the outbox: %v", err)
	}
	if err := e.s.RO.QueryRowContext(ctx, `select count(*) from forms_forms`).Scan(&n); state(err) != "42501" {
		t.Errorf("select on the form table: %v", err)
	}
	// ... but the function works (it runs with its owner's rights)
	id, err := e.repo.Submit(ctx, formsrepositories.Submission{FormSlug: "contact", Locale: "en", DataJSON: []byte(`{"name":"Direct call ` + marker(t) + `"}`)})
	if err != nil || len(id) != 36 {
		t.Fatalf("call: %q %v", id, err)
	}
}

func TestSubmitFunctionErrorsMapToSentinels(t *testing.T) {
	e := wire(t)
	ctx := context.Background()
	ownerExec(t, e.s, limitForm)
	t.Cleanup(func() { dropLimitForm(t, e) })

	// P0002: not published / not there
	if _, err := e.repo.Submit(ctx, formsrepositories.Submission{FormSlug: "no-such-form", DataJSON: []byte(`{}`)}); !errors.Is(err, formsrepositories.ErrFormUnavailable) {
		t.Errorf("unknown form: %v", err)
	}
	// 22023: the payload is not an object ...
	if _, err := e.repo.Submit(ctx, formsrepositories.Submission{FormSlug: "contact", DataJSON: []byte(`[1,2]`)}); !errors.Is(err, formsrepositories.ErrInvalidPayload) {
		t.Errorf("array payload: %v", err)
	}
	// ... or over max_payload_bytes (the 'lim' form allows 256)
	big := []byte(`{"name":"` + strings.Repeat("a", 400) + `"}`)
	if _, err := e.repo.Submit(ctx, formsrepositories.Submission{FormSlug: "lim", DataJSON: big}); !errors.Is(err, formsrepositories.ErrInvalidPayload) {
		t.Errorf("oversize payload: %v", err)
	}
	// anything else is reduced to a SQLSTATE: the payload never reaches an error text
	_, err := e.repo.Submit(ctx, formsrepositories.Submission{FormSlug: "contact", DataJSON: []byte(`{"name":"SECRET-PAYLOAD"`)})
	if err == nil || errors.Is(err, formsrepositories.ErrFormUnavailable) || strings.Contains(err.Error(), "SECRET-PAYLOAD") {
		t.Errorf("malformed json: %v", err)
	}
	// the repository is also used through the service: an oversize cleaned payload becomes 413, never a 500
	// (checked in the services tests); here the form's own limit is honoured end to end by TestDailyLimit...
	_ = iphash.SaltTTL
}
