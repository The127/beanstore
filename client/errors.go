package client

import (
	"google.golang.org/grpc/status"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
)

// WrongState extracts the strict-state refusal detail from an error.
// It names the volume and the state the node found, so a driver can
// decide between satisfied and bug without another round trip.
func WrongState(err error) (*beanstorev1.WrongState, bool) {
	responseStatus, ok := status.FromError(err)
	if !ok {
		return nil, false
	}

	for _, detail := range responseStatus.Details() {
		if wrongState, ok := detail.(*beanstorev1.WrongState); ok {
			return wrongState, true
		}
	}

	return nil, false
}
