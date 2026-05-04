package auth

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil || !ok {
		t.Fatalf("expected ok=true err=nil, got ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("wrong password", hash)
	if err != nil {
		t.Fatalf("verify wrong: %v", err)
	}
	if ok {
		t.Fatal("expected mismatch on wrong password")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"not-a-phc",
		"$argon2i$v=19$m=65536,t=3,p=2$YQ$YQ", // wrong variant
	}
	for _, c := range cases {
		if _, err := VerifyPassword("any", c); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}
