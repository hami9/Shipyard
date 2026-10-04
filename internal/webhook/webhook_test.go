package webhook

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitHub's published test vector [GH-VALIDATE].
const (
	vectorSecret = "It's a Secret to Everybody"
	vectorBody   = "Hello, World!"
	vectorSig    = "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
)

func TestSignMatchesGitHub(t *testing.T) {
	if got := Sign([]byte(vectorSecret), []byte(vectorBody)); got != vectorSig {
		t.Fatalf("Sign = %s, want %s", got, vectorSig)
	}
	if err := Verify([]byte(vectorSecret), []byte(vectorBody), vectorSig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyRejects(t *testing.T) {
	secret, body := []byte(vectorSecret), []byte(vectorBody)
	hexSig := strings.TrimPrefix(vectorSig, "sha256=")
	for name, tc := range map[string]struct {
		secret, body []byte
		header       string
		want         error
	}{
		"no header":       {secret, body, "", ErrUnsigned},
		"other secret":    {[]byte("It's a Secret to Somebody"), body, vectorSig, ErrSignature},
		"tampered body":   {secret, []byte("Hello, World?"), vectorSig, ErrSignature},
		"empty body":      {secret, nil, vectorSig, ErrSignature},
		"no prefix":       {secret, body, hexSig, ErrSignature},
		"sha1 prefix":     {secret, body, "sha1=" + hexSig, ErrSignature},
		"upper prefix":    {secret, body, "SHA256=" + hexSig, ErrSignature},
		"not hex":         {secret, body, "sha256=" + strings.Repeat("zz", 32), ErrSignature},
		"truncated":       {secret, body, vectorSig[:len(vectorSig)-2], ErrSignature},
		"extended":        {secret, body, vectorSig + "00", ErrSignature},
		"space around":    {secret, body, " " + vectorSig, ErrSignature},
		"prefix only":     {secret, body, "sha256=", ErrSignature},
		"empty secret":    {nil, body, Sign(nil, body), ErrNoSecret},
		"two signatures":  {secret, body, vectorSig + "," + vectorSig, ErrSignature},
		"one bit flipped": {secret, body, "sha256=657107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17", ErrSignature},
	} {
		t.Run(name, func(t *testing.T) {
			if err := Verify(tc.secret, tc.body, tc.header); !errors.Is(err, tc.want) {
				t.Fatalf("Verify = %v, want %v", err, tc.want)
			}
		})
	}
}

const sha = "0123456789abcdef0123456789abcdef01234567"

func TestParsePush(t *testing.T) {
	for name, tc := range map[string]struct {
		body   string
		want   Push
		ignore string
		err    bool
	}{
		"branch": {
			body: `{"ref":"refs/heads/main","before":"` + strings.Repeat("1", 40) + `","after":"` + sha + `","deleted":false,
				"repository":{"id":42,"full_name":"octo/app"},"head_commit":{"id":"` + sha + `","message":"x"}}`,
			want: Push{RepositoryID: 42, Repository: "octo/app", Ref: "refs/heads/main", Branch: "main", After: sha},
		},
		"branch with a slash": {
			body: `{"ref":"refs/heads/release/v1","after":"` + sha + `","repository":{"id":7,"full_name":"o/r"}}`,
			want: Push{RepositoryID: 7, Repository: "o/r", Ref: "refs/heads/release/v1", Branch: "release/v1", After: sha},
		},
		"sha256 repository": {
			body: `{"ref":"refs/heads/main","after":"` + strings.Repeat("ab", 32) + `","repository":{"id":1,"full_name":"o/r"}}`,
			want: Push{RepositoryID: 1, Repository: "o/r", Ref: "refs/heads/main", Branch: "main", After: strings.Repeat("ab", 32)},
		},
		"deleted branch": {
			body:   `{"ref":"refs/heads/old","after":"` + strings.Repeat("0", 40) + `","deleted":true,"repository":{"id":1,"full_name":"o/r"}}`,
			ignore: "the push deleted the ref",
		},
		"tag": {
			body:   `{"ref":"refs/tags/v1.0.0","after":"` + sha + `","repository":{"id":1,"full_name":"o/r"}}`,
			ignore: "the ref is not a branch",
		},
		"bare heads prefix":  {body: `{"ref":"refs/heads/","after":"` + sha + `","repository":{"id":1}}`, ignore: "the ref is not a branch"},
		"not json":           {body: `ref=refs/heads/main`, err: true},
		"trailing data":      {body: `{"ref":"refs/heads/main","after":"` + sha + `","repository":{"id":1}} {}`, err: true},
		"no repository":      {body: `{"ref":"refs/heads/main","after":"` + sha + `"}`, err: true},
		"negative repo id":   {body: `{"ref":"refs/heads/main","after":"` + sha + `","repository":{"id":-1}}`, err: true},
		"short sha":          {body: `{"ref":"refs/heads/main","after":"abc123","repository":{"id":1}}`, err: true},
		"upper-case sha":     {body: `{"ref":"refs/heads/main","after":"` + strings.ToUpper(sha) + `","repository":{"id":1}}`, err: true},
		"zero sha, live ref": {body: `{"ref":"refs/heads/main","after":"` + strings.Repeat("0", 40) + `","repository":{"id":1}}`, err: true},
		"control in ref":     {body: `{"ref":"refs/heads/ma\nin","after":"` + sha + `","repository":{"id":1}}`, err: true},
		"long ref":           {body: `{"ref":"refs/heads/` + strings.Repeat("a", 250) + `","after":"` + sha + `","repository":{"id":1}}`, err: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, ignore, err := ParsePush([]byte(tc.body))
			if (err != nil) != tc.err {
				t.Fatalf("err = %v, want error %v", err, tc.err)
			}
			if err == nil && (got != tc.want || ignore != tc.ignore) {
				t.Fatalf("ParsePush = %+v, %q; want %+v, %q", got, ignore, tc.want, tc.ignore)
			}
		})
	}
}

func TestValidDelivery(t *testing.T) {
	for id, want := range map[string]bool{
		"72d3162e-cc78-11e3-81ab-4c9367dc0958": true,
		"abc":                                  true,
		"":                                     false,
		strings.Repeat("a", 65):                false,
		"72d3162e cc78":                        false,
		"../x":                                 false,
		"a\nb":                                 false,
	} {
		if got := ValidDelivery(id); got != want {
			t.Errorf("ValidDelivery(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestLoadSecret(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil { // umask may have narrowed it
			t.Fatal(err)
		}
		return p
	}
	secret := "0123456789abcdef0123456789abcdef"
	got, err := LoadSecret(write("ok", secret+"\n", 0o640))
	if err != nil || string(got) != secret {
		t.Fatalf("LoadSecret = %q, %v", got, err)
	}
	for name, path := range map[string]string{
		"world-readable": write("open", secret, 0o644),
		"too short":      write("short", "0123456789abcde\n", 0o600),
		"empty":          write("empty", "\n", 0o600),
		"missing":        filepath.Join(dir, "none"),
		"a directory":    dir,
	} {
		if _, err := LoadSecret(path); err == nil {
			t.Errorf("%s: LoadSecret accepted it", name)
		} else if strings.Contains(err.Error(), secret) {
			t.Errorf("%s: the error shows the secret: %v", name, err)
		}
	}
}
