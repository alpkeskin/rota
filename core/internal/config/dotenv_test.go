package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	os.WriteFile(p, []byte("# comment\nDOTENV_A=1\nexport DOTENV_B=\"two words\"\nDOTENV_C='x'\nDOTENV_SET=from-file\nBROKEN LINE\n"), 0o600)
	t.Setenv("DOTENV_SET", "from-env")
	t.Setenv("DOTENV_A", "")
	loadDotEnv(p)
	for k, want := range map[string]string{"DOTENV_A": "1", "DOTENV_B": "two words", "DOTENV_C": "x", "DOTENV_SET": "from-env"} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	loadDotEnv(filepath.Join(dir, "missing")) // must not panic
}
