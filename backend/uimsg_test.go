package main

import (
	"os"
	"strings"
	"testing"
)

// The gateway and direct listener share these errors with the Vue client.
func TestConsoleErrorTranslations(t *testing.T) {
	data, err := os.ReadFile("../frontend/src/composables/useI18n.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"err_admin_required", "err_login_required", "err_origin_denied"} {
		if strings.Count(string(data), code) < 2 {
			t.Errorf("missing bilingual translation for %s", code)
		}
	}
}
