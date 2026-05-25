package base62

import "testing"

func TestRoundTrip(t *testing.T) {
	cases := []uint64{0, 1, 61, 62, 3844, 123456789, 1<<63 - 1}
	for _, tc := range cases {
		got, err := Decode(Encode(tc))
		if err != nil {
			t.Fatalf("decode %d: %v", tc, err)
		}
		if got != tc {
			t.Fatalf("round trip %d got %d", tc, got)
		}
	}
}

func TestDecodeRejectsInvalidInput(t *testing.T) {
	if _, err := Decode("abc-"); err == nil {
		t.Fatal("expected invalid input error")
	}
}
