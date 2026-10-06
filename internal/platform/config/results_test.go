package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadResults(t *testing.T) {
	t.Run("a day, up to a mebibyte, by default", func(t *testing.T) {
		t.Setenv("RESULT_RETENTION", "")
		t.Setenv("RESULT_MAX_BYTES", "")
		var c ResultsConfig
		if err := loadResults(&c); err != nil || c.Retention != 24*time.Hour || c.MaxBytes != 1<<20 {
			t.Errorf("%v %+v", err, c)
		}
	})
	t.Run("a chosen retention and size", func(t *testing.T) {
		t.Setenv("RESULT_RETENTION", " 6h ")
		t.Setenv("RESULT_MAX_BYTES", "65536")
		var c ResultsConfig
		if err := loadResults(&c); err != nil || c.Retention != 6*time.Hour || c.MaxBytes != 65536 {
			t.Errorf("%v %+v", err, c)
		}
	})
	for _, off := range []string{"off", "OFF", "0", "none"} {
		t.Run("keeps nothing for "+off, func(t *testing.T) {
			t.Setenv("RESULT_RETENTION", off)
			var c ResultsConfig
			if err := loadResults(&c); err != nil || c.Retention != 0 {
				t.Errorf("%v %+v", err, c)
			}
		})
	}
	for _, bad := range []string{"forever", "-1h", "169h", "30d", "12"} {
		t.Run("refuses retention "+bad, func(t *testing.T) {
			t.Setenv("RESULT_RETENTION", bad)
			var c ResultsConfig
			if err := loadResults(&c); err == nil || !strings.Contains(err.Error(), "RESULT_RETENTION") {
				t.Errorf("%v", err)
			}
		})
	}
	for _, bad := range []string{"big", "100", "-5", "17000000", "1.5"} {
		t.Run("refuses size "+bad, func(t *testing.T) {
			t.Setenv("RESULT_RETENTION", "")
			t.Setenv("RESULT_MAX_BYTES", bad)
			var c ResultsConfig
			if err := loadResults(&c); err == nil || !strings.Contains(err.Error(), "RESULT_MAX_BYTES") {
				t.Errorf("%v", err)
			}
		})
	}
}
