package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func valid() map[string]string {
	return map[string]string{
		"APP_ENV":              "development",
		"DATABASE_URL":         "postgres://u:p@localhost/db",
		"ADMIN_USERNAME":       "admin",
		"ADMIN_PASSWORD_HASH":  "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"TOKEN_ENCRYPTION_KEY": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	}
}

func TestLoadValidDefaults(t *testing.T) {
	c, err := LoadFrom(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != "8080" || c.IntegrationMode != ModeMock || c.PublishingEnabled {
		t.Fatalf("defaults = port %s mode %s publishing %v", c.Port, c.IntegrationMode, c.PublishingEnabled)
	}
	if c.X.RedirectURI != "http://localhost:8080/api/x/oauth/callback" {
		t.Fatalf("redirect = %s", c.X.RedirectURI)
	}
	if c.R2.Prefix != "x-automation/" {
		t.Fatalf("prefix = %s", c.R2.Prefix)
	}
}

func TestProductionRefusesMissingAuthAndMock(t *testing.T) {
	m := valid()
	m["APP_ENV"] = "production"
	m["APP_BASE_URL"] = "http://insecure.example.com"
	m["INTEGRATION_MODE"] = "mock"
	delete(m, "ADMIN_PASSWORD_HASH")
	_, err := LoadFrom(env(m))
	e, ok := AsError(err)
	if !ok {
		t.Fatalf("want config error, got %v", err)
	}
	want := map[string]bool{"APP_BASE_URL": false, "INTEGRATION_MODE": false, "ADMIN_PASSWORD_HASH": false}
	for _, p := range e.Problems {
		if _, ok := want[p.Var]; ok {
			want[p.Var] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("expected problem for %s, got %v", k, e)
		}
	}
}

func TestPlaintextPasswordRejected(t *testing.T) {
	m := valid()
	m["ADMIN_PASSWORD_HASH"] = "hunter2"
	_, err := LoadFrom(env(m))
	if err == nil || !strings.Contains(err.Error(), "Argon2id") {
		t.Fatalf("want argon2id error, got %v", err)
	}
}

func TestPartialR2IsAnError(t *testing.T) {
	m := valid()
	m["R2_BUCKET"] = "b"
	_, err := LoadFrom(env(m))
	if err == nil || !strings.Contains(err.Error(), "R2_*") {
		t.Fatalf("want partial R2 error, got %v", err)
	}
}

func TestMockWithPublishingIsRejected(t *testing.T) {
	m := valid()
	m["PUBLISHING_ENABLED"] = "true"
	if _, err := LoadFrom(env(m)); err == nil {
		t.Fatal("mock + publishing should be rejected")
	}
}

func TestBadKeyLength(t *testing.T) {
	m := valid()
	m["TOKEN_ENCRYPTION_KEY"] = "c2hvcnQ="
	if _, err := LoadFrom(env(m)); err == nil {
		t.Fatal("short key should be rejected")
	}
}

func TestKoDeployDatabaseVariables(t *testing.T) {
	m := valid()
	m["DATABASE_URL"] = "postgresql+psycopg2://u:p@postgres:5432/app"
	c, err := LoadFrom(env(m))
	if err != nil || c.DatabaseURL != "postgresql://u:p@postgres:5432/app" {
		t.Fatalf("sqlalchemy url = %q err=%v", c.DatabaseURL, err)
	}
	delete(m, "DATABASE_URL")
	m["DB_HOST"], m["DB_NAME"], m["DB_USER"], m["DB_PASSWORD"] = "postgres", "app", "u", "p@ss/word"
	c, err = LoadFrom(env(m))
	if err != nil || c.DatabaseURL != "postgres://u:p%40ss%2Fword@postgres:5432/app?sslmode=disable" {
		t.Fatalf("db_* url = %q err=%v", c.DatabaseURL, err)
	}
}

func TestKoDeployS3Fallback(t *testing.T) {
	m := valid()
	m["S3_ENDPOINT"], m["S3_ACCESS_KEY"], m["S3_SECRET_KEY"], m["S3_BUCKET"] = "https://acct.r2.cloudflarestorage.com", "ak", "sk", "kd-app"
	m["S3_PUBLIC_BASE_URL"] = "https://pub-123.r2.dev"
	c, err := LoadFrom(env(m))
	if err != nil || !c.R2.Configured() || c.R2.Bucket != "kd-app" || c.R2.PublicBaseURL != "https://pub-123.r2.dev" {
		t.Fatalf("s3 fallback = %+v err=%v", c.R2, err)
	}
}
