package storage

import "testing"

func TestGlobalIDRoundTrip(t *testing.T) {
	for _, total := range []uint32{1, 16, 64} {
		for bucket := uint32(0); bucket < total; bucket++ {
			globalID, err := ComposeGlobalID(12345, bucket, total)
			if err != nil {
				t.Fatalf("compose: %v", err)
			}
			localID, gotBucket, err := SplitGlobalID(globalID, total)
			if err != nil {
				t.Fatalf("split: %v", err)
			}
			if localID != 12345 || gotBucket != bucket {
				t.Fatalf("total=%d bucket=%d got local=%d bucket=%d", total, bucket, localID, gotBucket)
			}
		}
	}
}
