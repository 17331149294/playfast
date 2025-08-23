package utils

import (
	"testing"
)

func TestName(t *testing.T) {
	info, err := GetDefaultNetworkInfo()
	if err != nil {
		t.Fatal(err)
	}
	t.Log(info)
	firstUnreachable, err := scanCIDR("192.168.2.0/24")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(firstUnreachable)
	t.Log(RandIP())
}
