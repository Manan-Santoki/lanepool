package auth

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(h, "wrong") || VerifyPassword("garbage", "correct horse") {
		t.Fatal("invalid password accepted")
	}
	h2, _ := HashPassword("correct horse")
	if h == h2 {
		t.Fatal("hashes are not salted")
	}
}

func TestTokens(t *testing.T) {
	a, b := RandomToken("lp_", 24), RandomToken("lp_", 24)
	if a == b || len(a) < 30 || a[:3] != "lp_" {
		t.Fatalf("bad tokens %q %q", a, b)
	}
	if HashToken(a) == HashToken(b) || HashToken(a) != HashToken(a) {
		t.Fatal("token hashing is wrong")
	}
}

func TestValidatePassword(t *testing.T) {
	for _, bad := range []string{"short", "has space in it", string(make([]byte, 200))} {
		if ValidatePassword(bad) == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if ValidatePassword("v9MV4kwuU6OVuZcq") != nil {
		t.Fatal("rejected a good password")
	}
}
