//go:build integration

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
	"github.com/The127/beanstore/internal/api"
	"github.com/The127/beanstore/internal/testpki"
)

func TestIntegrationSecureMove(t *testing.T) {
	pki := testpki.New(t)
	orchestrator := testpki.ClientCredentials(t, pki.Leaf(t, "orchestrator", api.RoleOrchestrator))

	sourceClient, sourceCfg := provision(t)
	sourceCfg.TLS = pki.Leaf(t, "node-a", api.RoleNode)
	source := serveAs(t, sourceClient, sourceCfg, orchestrator)

	targetClient, targetCfg := provision(t)
	targetCfg.TLS = pki.Leaf(t, "node-b", api.RoleNode)
	target := serveAs(t, targetClient, targetCfg, orchestrator)
	ctx := t.Context()

	// a plaintext client cannot connect, a node certificate cannot
	// touch the control plane
	plain, err := grpc.NewClient(target.address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = plain.Close() })
	_, err = beanstorev1.NewVolumeServiceClient(plain).ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	assert.Equal(t, codes.Unavailable, status.Code(err))

	nodeConn, err := grpc.NewClient(target.address,
		grpc.WithTransportCredentials(testpki.ClientCredentials(t, sourceCfg.TLS)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeConn.Close() })
	_, err = beanstorev1.NewVolumeServiceClient(nodeConn).ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	// the full move runs under mutual tls, node to node included
	_, err = source.volumes.CreateVolume(ctx, &beanstorev1.CreateVolumeRequest{
		VolumeId:    "vol-1",
		SizeBytes:   16 << 20,
		OperationId: "op-create",
	})
	require.NoError(t, err)
	waitDone(t, source.operations, "op-create")

	attach, err := source.volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)
	pattern := patternFile(t, 2<<20, 211)
	require.NoError(t, sudoRun(ctx, "dd", "if="+pattern.path, "of="+attach.DevicePath, "bs=1M", "conv=fsync"))
	_, err = source.volumes.Detach(ctx, &beanstorev1.DetachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)

	_, err = target.volumes.PrepareReceive(ctx, &beanstorev1.PrepareReceiveRequest{
		VolumeId:   "vol-1",
		SizeBytes:  16 << 20,
		TransferId: "tr-secure",
	})
	require.NoError(t, err)

	_, err = source.volumes.PushVolume(ctx, &beanstorev1.PushVolumeRequest{
		VolumeId:      "vol-1",
		TransferId:    "tr-secure",
		TargetAddress: target.address,
		OperationId:   "op-push",
	})
	require.NoError(t, err)
	waitDone(t, source.operations, "op-push")

	list, err := source.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	require.Len(t, list.Volumes, 1)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_RETIRED, list.Volumes[0].State)

	list, err = target.volumes.ListVolumes(ctx, &beanstorev1.ListVolumesRequest{})
	require.NoError(t, err)
	require.Len(t, list.Volumes, 1)
	assert.Equal(t, beanstorev1.VolumeState_VOLUME_STATE_READY, list.Volumes[0].State)

	attach, err = target.volumes.Attach(ctx, &beanstorev1.AttachRequest{VolumeId: "vol-1"})
	require.NoError(t, err)
	assert.Equal(t, pattern.bytes, readDevice(t, attach.DevicePath, len(pattern.bytes)),
		"the content crossed the encrypted wire intact")
}
