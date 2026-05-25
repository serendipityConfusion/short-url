package storage

import "errors"

var ErrInvalidGlobalID = errors.New("invalid global id")

func ComposeGlobalID(localID uint64, bucket uint32, totalBuckets uint32) (uint64, error) {
	if localID == 0 || totalBuckets == 0 || bucket >= totalBuckets {
		return 0, ErrInvalidGlobalID
	}
	return (localID-1)*uint64(totalBuckets) + uint64(bucket) + 1, nil
}

func SplitGlobalID(globalID uint64, totalBuckets uint32) (uint64, uint32, error) {
	if globalID == 0 || totalBuckets == 0 {
		return 0, 0, ErrInvalidGlobalID
	}
	zeroBased := globalID - 1
	localID := zeroBased/uint64(totalBuckets) + 1
	bucket := uint32(zeroBased % uint64(totalBuckets))
	return localID, bucket, nil
}
