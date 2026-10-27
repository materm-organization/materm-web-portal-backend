package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// GORM decides whether to print bound values by asserting db.Logger to gorm.ParamsFilter. The wrapper must
// keep satisfying it (and keep ParameterizedQueries on), also after GORM derives a logger with another level:
// form submissions travel as bound values.
func TestGormLoggerHidesBoundValues(t *testing.T) {
	for name, l := range map[string]logger.Interface{
		"new":     newGormLoggerTo(io.Discard),
		"LogMode": newGormLoggerTo(io.Discard).LogMode(logger.Warn),
	} {
		f, ok := l.(gorm.ParamsFilter)
		if !ok {
			t.Errorf("%s: the logger no longer satisfies gorm.ParamsFilter, ParameterizedQueries would be ignored", name)
			continue
		}
		sql, vars := f.ParamsFilter(context.Background(), "select forms_submit_form($1)", "visitor@example.test")
		if vars != nil || strings.Contains(sql, "visitor@example.test") {
			t.Errorf("%s: bound values leak: %q %v", name, sql, vars)
		}
	}
}

func TestGormLoggerLogsErrorsNotInfo(t *testing.T) {
	var buf bytes.Buffer
	l := newGormLoggerTo(&buf)
	l.Info(context.Background(), "an info line")
	if buf.Len() != 0 {
		t.Errorf("info must not be logged: %q", buf.String())
	}
	l.Trace(context.Background(), time.Now(), func() (string, int64) { return "select 1", 0 }, errors.New("boom"))
	if !strings.Contains(buf.String(), "select 1") {
		t.Errorf("an SQL error must be logged: %q", buf.String())
	}
}

func TestSanitizeRemovesPassword(t *testing.T) {
	c := DBConnection{Password: "hunter2-secret"}
	err := sanitize(errors.New("failed: password=hunter2-secret host=x"), c)
	if strings.Contains(err.Error(), "hunter2-secret") || !strings.Contains(err.Error(), "<password>") {
		t.Errorf("password not removed: %v", err)
	}
	orig := errors.New("no secret here")
	if sanitize(orig, c) != orig {
		t.Error("an error without the password must be returned as is")
	}
	if sanitize(nil, c) != nil {
		t.Error("nil stays nil")
	}
}
