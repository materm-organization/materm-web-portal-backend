package dbtest

import (
	"errors"
	"strings"
	"testing"
)

func TestGuardRefusesUserDB(t *testing.T) {
	refused := map[string]string{
		"materm db, scratch port":    "postgres://u:p@127.0.0.1:55433/materm?sslmode=disable",
		"materm db, upper case":      "postgres://u:p@127.0.0.1:55433/MATERM?sslmode=disable",
		"user port localhost":        "postgres://u:p@localhost:5432/postgres?sslmode=disable",
		"user port loopback":         "postgres://u:p@127.0.0.1:5432/materm_test_x?sslmode=disable",
		"user port ipv6":             "postgres://u:p@[::1]:5432/postgres?sslmode=disable",
		"user port remote host":      "postgres://u:p@db.internal:5432/postgres?sslmode=disable",
		"default port is 5432":       "postgres://u:p@127.0.0.1/postgres?sslmode=disable",
		"keyword form, materm db":    "host=127.0.0.1 port=55433 user=u password=p dbname=materm",
		"keyword form, user port":    "host=127.0.0.1 port=5432 user=u password=p dbname=other",
		"fallback host on user port": "postgres://u:p@127.0.0.1:55433,127.0.0.1:5432/postgres?sslmode=disable",
		"empty":                      "",
		"unparseable":                "postgres://u:p@127.0.0.1:notaport/x",
	}
	for name, dsn := range refused {
		t.Run(name, func(t *testing.T) {
			err := Guard(dsn)
			if err == nil {
				t.Fatalf("Guard accepted %q", name)
			}
			if !errors.Is(err, ErrRefused) {
				t.Fatalf("not an ErrRefused: %v", err)
			}
		})
	}

	accepted := []string{
		"postgres://u:p@127.0.0.1:55433/postgres?sslmode=disable",
		"postgres://u:p@127.0.0.1:55433/materm_test_ab12?sslmode=disable",
		"host=127.0.0.1 port=55433 user=u password=p dbname=postgres",
	}
	for _, dsn := range accepted {
		if err := Guard(dsn); err != nil {
			t.Fatalf("Guard refused a scratch DSN: %v", err)
		}
	}
}

func TestGuardRefusesAppConfiguredTarget(t *testing.T) {
	t.Setenv("DATABASE_ADMIN_HOST", "127.0.0.1")
	t.Setenv("DATABASE_ADMIN_PORT", "55433")
	t.Setenv("DATABASE_ADMIN_DBNAME", "app_db")
	if err := Guard("postgres://u:p@127.0.0.1:55433/app_db?sslmode=disable"); err == nil {
		t.Fatal("Guard accepted the application's own database")
	}
	if err := Guard("postgres://u:p@127.0.0.1:55433/postgres?sslmode=disable"); err == nil {
		t.Fatal("Guard accepted the application's own host:port")
	}
}

func TestGuardMessageNeverContainsSecrets(t *testing.T) {
	err := Guard("postgres://user:s3cr3t-pass@127.0.0.1:5432/materm")
	if err == nil {
		t.Fatal("expected refusal")
	}
	for _, leak := range []string{"s3cr3t-pass", "user:"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("guard message leaks %q", leak)
		}
	}
}

func fakeRead(marker, db string) func(dest ...any) error {
	return func(dest ...any) error {
		*(dest[0].(*string)) = marker
		*(dest[1].(*string)) = db
		return nil
	}
}

func TestCheckScratchClusterRefusesClusterWithoutMarker(t *testing.T) {
	if err := checkScratch(fakeRead("", "postgres")); !errors.Is(err, ErrRefused) {
		t.Fatalf("cluster without GUC was accepted: %v", err)
	}
	if err := checkScratch(fakeRead("off", "postgres")); !errors.Is(err, ErrRefused) {
		t.Fatalf("cluster with GUC=off was accepted: %v", err)
	}
	if err := checkScratch(fakeRead("on", "materm")); !errors.Is(err, ErrRefused) {
		t.Fatalf("database materm was accepted: %v", err)
	}
	if err := checkScratch(fakeRead("on", "postgres")); err != nil {
		t.Fatalf("scratch cluster refused: %v", err)
	}
	if err := checkScratch(func(...any) error { return errors.New("boom") }); !errors.Is(err, ErrRefused) {
		t.Fatalf("unreadable marker was accepted: %v", err)
	}
}
