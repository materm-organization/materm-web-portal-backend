package services

import (
	"time"
)

// Spam scores (doc/08 section 6.3). The database stores a submission with status 'spam' from 80 up, and the
// worker sends no e-mail for it; the visitor (a bot) still gets the same 200 as everybody.
const (
	ScoreHoneypot  int16 = 100
	ScoreTooFast   int16 = 90
	ScoreClockSkew int16 = 90
)

// Reason names why a submission was scored.
type Reason string

// Reasons of a spam score. They exist for tests and for metrics; they are never sent to the client.
const (
	ReasonHoneypot  Reason = "honeypot"
	ReasonTooFast   Reason = "too_fast"
	ReasonClockSkew Reason = "clock_skew"
)

// SpamInput is what the score is computed from.
type SpamInput struct {
	// Honeypot is the value of the hidden field (the body's honeypot, or the form's honeypot key found in
	// data).
	Honeypot string
	// Elapsed is how long the visitor took (elapsed_ms of the body, 0 when absent or when the server action
	// could not verify the render stamp).
	Elapsed time.Duration
	// MinFill is the form's min_fill_seconds.
	MinFill time.Duration
	// RenderedAt is the optional client timestamp (nil = not sent). Now is the server clock; MaxSkew is
	// forms.maxClockSkew.
	RenderedAt *time.Time
	// StampInvalid: a rendered_at was sent but is not a timestamp.
	StampInvalid bool
	Now          time.Time
	MaxSkew      time.Duration
}

// Verdict is the outcome of Score.
type Verdict struct {
	Score   int16
	Reasons []Reason
}

// Score computes the spam score: the highest of the triggers that fired.
//
//   - a non-empty honeypot: 100;
//   - Elapsed shorter than MinFill: 90 (a human cannot fill the form that fast; a missing stamp is 0 and so
//     scores here too, as the server action sends 0 for a stamp it could not verify);
//   - RenderedAt in the future by more than MaxSkew, or not a timestamp at all: 90 (a stamp from a clock that
//     far ahead cannot be honest; a stamp in the past or within the skew is fine - a stale page is not spam).
//     An absent stamp passes: the field is optional.
func Score(in SpamInput) Verdict {
	var v Verdict
	raise := func(score int16, r Reason) {
		v.Reasons = append(v.Reasons, r)
		if score > v.Score {
			v.Score = score
		}
	}
	if in.Honeypot != "" {
		raise(ScoreHoneypot, ReasonHoneypot)
	}
	if in.Elapsed < in.MinFill {
		raise(ScoreTooFast, ReasonTooFast)
	}
	if in.StampInvalid || (in.RenderedAt != nil && in.RenderedAt.After(in.Now.Add(in.MaxSkew))) {
		raise(ScoreClockSkew, ReasonClockSkew)
	}
	return v
}
