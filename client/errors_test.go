package client

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
)

func TestWrongStateRoundTrip(t *testing.T) {
	refusal, err := status.New(codes.FailedPrecondition, "volume vol-1 is in state attached").
		WithDetails(&beanstorev1.WrongState{
			VolumeId: "vol-1",
			Found:    beanstorev1.VolumeState_VOLUME_STATE_ATTACHED,
		})
	if err != nil {
		t.Fatal(err)
	}

	wrongState, ok := WrongState(refusal.Err())
	if !ok {
		t.Fatal("detail not found")
	}
	if wrongState.VolumeId != "vol-1" {
		t.Errorf("volume id %q", wrongState.VolumeId)
	}
	if wrongState.Found != beanstorev1.VolumeState_VOLUME_STATE_ATTACHED {
		t.Errorf("found state %v", wrongState.Found)
	}
}

func TestWrongStateAbsent(t *testing.T) {
	if _, ok := WrongState(status.Error(codes.NotFound, "gone")); ok {
		t.Error("detail on a plain status")
	}
	if _, ok := WrongState(errors.New("not a status")); ok {
		t.Error("detail on a non status error")
	}
}
