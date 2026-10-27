// Generates parity_golden.json: the answers of the REAL contact-validation.ts of the portal frontend for a
// corpus of hostile and ordinary inputs. The Go test (validate_test.go) must agree with every row.
//
//   node gen_parity.mjs ../../../../../materm-web-portal-frontend/src/components/contact/contact-validation.ts
//
// The TypeScript file is copied to a scratch directory first (node strips the types itself) so nothing is
// written next to the frontend. Re-run it whenever contact-validation.ts changes.
import { mkdtempSync, copyFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const src = process.argv[2];
if (!src) throw new Error("usage: node gen_parity.mjs <path to contact-validation.ts>");
const dir = mkdtempSync(join(tmpdir(), "parity-"));
const copy = join(dir, "contact-validation.mts");
copyFileSync(src, copy);
const cv = await import(pathToFileURL(copy).href);

const ok = { name: "Noy Phommasan", email: "noy@example.com", phone: "+856 20 5555 1234", topic: "booking", message: "Hello, I would like to book a tent for two nights." };
const cases = [];
const add = (label, over) => cases.push({ label, input: { ...ok, ...over } });

add("valid", {});
add("valid, phone empty", { phone: "" });
add("all empty", { name: "", email: "", phone: "", topic: "", message: "" });
add("all missing", { name: undefined, email: undefined, phone: undefined, topic: undefined, message: undefined });
// name
add("name 1 char", { name: "A" });
add("name 2 chars", { name: "Ab" });
add("name only spaces", { name: "     " });
add("name emoji (2 UTF-16 units)", { name: "\u{1F600}" });
add("name two emoji", { name: "\u{1F600}\u{1F600}" });
add("name lao", { name: "ກ" });
add("name lao two", { name: "ກຂ" });
add("name 100", { name: "a".repeat(100) });
add("name 101", { name: "a".repeat(101) });
add("name 150 emoji (cut mid-way)", { name: "\u{1F600}".repeat(60) });
add("name tabs and newlines", { name: "Noy\t\n  Phommasan\r\nX" });
add("name control chars", { name: "No\u0000y\u0007 Ph\u001fom" });
add("name C1 controls", { name: "No\u0085y\u009f Phom" });
add("name nbsp edges", { name: "  Noy  " });
add("name zwnbsp edges", { name: "﻿﻿Noy﻿" });
add("name ideographic space edges", { name: "　Noy　" });
add("name inner nbsp", { name: "Noy  P" });
add("name NFD e-acute", { name: "Ré" });
add("name NFD only combining", { name: "é" });
add("name number", { name: 12345 });
add("name null", { name: null });
add("name object", { name: { a: 1 } });
add("name array", { name: ["x", "y"] });
add("name boolean", { name: true });
// email
add("email no at", { email: "noy.example.com" });
add("email no dot", { email: "noy@example" });
add("email dot last", { email: "noy@example." });
add("email 1 char tld", { email: "noy@example.c" });
add("email 2 char tld", { email: "noy@example.co" });
add("email dot first in domain", { email: "noy@.co" });
add("email dot first then ok", { email: "noy@.com" });
add("email two ats", { email: "a@b@c.com" });
add("email empty local", { email: "@example.com" });
add("email inner space", { email: "no y@example.com" });
add("email inner nbsp", { email: "no y@example.com" });
add("email ideographic space", { email: "noy@exa　mple.com" });
add("email tab inside", { email: "noy@exa\tmple.com" });
add("email emoji tld", { email: "noy@example.\u{1F600}" });
add("email emoji domain", { email: "noy@\u{1F600}.\u{1F600}\u{1F600}" });
add("email dot then 1 emoji", { email: "noy@example.\u{1F600}" });
add("email trailing newline", { email: "noy@example.com\n" });
add("email upper", { email: "NOY@EXAMPLE.COM" });
add("email 254", { email: "a".repeat(240) + "@example.com" });
add("email 300", { email: "a".repeat(300) + "@example.com" });
add("email lao", { email: "ກ@ຂ.຃ຄ" });
add("email plus", { email: "noy+tag@example.co.uk" });
add("email multiple dots", { email: "a@b..c" });
add("email dot space", { email: "a@b. c" });
// phone
add("phone letters", { phone: "abc" });
add("phone digits", { phone: "0201234567" });
add("phone allowed chars", { phone: "+856 (20) 555-1234.5" });
add("phone with x", { phone: "+856 20 555 1234 x12" });
add("phone 30", { phone: "1".repeat(30) });
add("phone 31", { phone: "1".repeat(31) });
add("phone 40 digits", { phone: "1".repeat(40) });
add("phone nbsp", { phone: "020 1234" });
add("phone fullwidth digits", { phone: "０１" });
add("phone arabic digits", { phone: "٠١" });
add("phone only spaces", { phone: "   " });
add("phone newline inside", { phone: "020\n1234" });
add("phone number type", { phone: 856 });
// topic
add("topic booking", { topic: "booking" });
add("topic events", { topic: "events" });
add("topic camping", { topic: "camping" });
add("topic other", { topic: "other" });
add("topic unknown", { topic: "refund" });
add("topic upper", { topic: "Booking" });
add("topic padded", { topic: "  booking  " });
add("topic inner tab", { topic: "book\ting" });
add("topic long junk", { topic: "booking" + " ".repeat(40) + "x" });
add("topic 25 chars", { topic: "b".repeat(25) });
add("topic nbsp padded", { topic: " booking " });
add("topic number", { topic: 1 });
// message
add("message 9", { message: "123456789" });
add("message 10", { message: "1234567890" });
add("message 10 with edge spaces", { message: "   123456789   " });
add("message 9 plus newline", { message: "123456789\n" });
add("message 2000", { message: "m".repeat(2000) });
add("message 2001", { message: "m".repeat(2001) });
add("message 3000", { message: "m".repeat(3000) });
add("message 1999 + spaces", { message: "m".repeat(1999) + " x" });
add("message CRLF", { message: "line one\r\nline two\r\nline three" });
add("message lone CR", { message: "line one\rline two" });
add("message mixed breaks", { message: "a\r\n\r\nb\r\rc\n\nd" });
add("message tabs kept", { message: "col1\tcol2\tcol3 text" });
add("message controls removed", { message: "he\u0000llo\u0001 wor\u0007ld\u001b[0m text" });
add("message C1 removed", { message: "hello\u0085 world\u009f text" });
add("message DEL removed", { message: "hello\u007f world text here" });
add("message vertical tab/form feed", { message: "hello\u000b\u000c world text" });
add("message line separators kept", { message: "hello world text here" });
add("message nbsp edges", { message: "   hello world text  " });
add("message zwnbsp edges", { message: "﻿hello world text﻿" });
add("message inner double spaces kept", { message: "hello    world    text" });
add("message emoji", { message: "\u{1F600}\u{1F600}\u{1F600}\u{1F600}\u{1F600}" });
add("message emoji 4 (8 units)", { message: "\u{1F600}\u{1F600}\u{1F600}\u{1F600}" });
add("message 1000 emoji (cut at 2000 units)", { message: "\u{1F600}".repeat(1000) });
add("message 1001 emoji", { message: "\u{1F600}".repeat(1001) });
add("message NFD", { message: "Café au lait, très bon" });
add("message lao", { message: "ກຂ຃ຄ຅ຆງຈຉຊ" });
add("message html", { message: "<script>alert(1)</script> and more" });
add("message null", { message: null });
add("message number", { message: 1234567890 });
// combos
add("everything wrong", { name: "x", email: "y", phone: "z", topic: "w", message: "v" });
add("everything wrong 2", { name: "", email: "a@b", phone: "#", topic: "", message: "short" });
add("only message wrong", { message: "hi" });
add("unicode whitespace everywhere", { name: " Noy ", email: " noy@example.com ", phone: " 020 1234 ", topic: " other ", message: " hello world text " });

const rows = cases.map(({ label, input }) => {
  // undefined cannot travel in JSON; the Go side treats an absent key as "missing", like the frontend does
  const jsonInput = JSON.parse(JSON.stringify(input));
  const values = cv.sanitizeContactValues(input);
  const errors = cv.validateContact(values);
  return { label, input: jsonInput, sanitized: values, errors };
});
const out = join(dirname(fileURLToPath(import.meta.url)), "parity_golden.json");
writeFileSync(out, JSON.stringify({ source: "src/components/contact/contact-validation.ts", rows }, null, 1) + "\n");
console.log(`wrote ${rows.length} rows to ${out}`);
