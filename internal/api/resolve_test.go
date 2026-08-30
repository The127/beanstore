package api

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	beanstorev1 "github.com/The127/beanstore/client/gen/beanstore/v1"
)

func TestResolveRecoveredRetiresCommitted(t *testing.T) {
	target := &fakeTarget{listed: []*beanstorev1.Volume{{
		VolumeId: "vol-1", State: beanstorev1.VolumeState_VOLUME_STATE_READY,
	}}}
	fake := &fakeRunner{outputs: []string{pushLV("committing", ""), pushLV("committing", "")}}
	volumes := pushHarness(t, fake, target)

	volumes.resolveRecovered()

	assert.Eventually(t, func() bool {
		return strings.Contains(lastRetag(fake), "--addtag beanstore.state=retired")
	}, 5*time.Second, 10*time.Millisecond)
	retagged := lastRetag(fake)
	assert.Contains(t, retagged, "--deltag beanstore.state=committing")
	assert.Contains(t, retagged, "--deltag beanstore.transfer=tr-1")
}

func TestResolveRecoveredRevertsUncommitted(t *testing.T) {
	target := &fakeTarget{}
	fake := &fakeRunner{outputs: []string{pushLV("committing", ""), pushLV("committing", "")}}
	volumes := pushHarness(t, fake, target)

	volumes.resolveRecovered()

	assert.Eventually(t, func() bool {
		return strings.Contains(lastRetag(fake), "--addtag beanstore.state=ready")
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, 1, target.abortCount(), "the dead transfer was aborted")
}

func TestResolveRecoveredRetriesUntilTargetAnswers(t *testing.T) {
	target := &fakeTarget{
		listErrs: 1,
		listed: []*beanstorev1.Volume{{
			VolumeId: "vol-1", State: beanstorev1.VolumeState_VOLUME_STATE_READY,
		}},
	}
	fake := &fakeRunner{outputs: []string{pushLV("committing", ""), pushLV("committing", "")}}
	volumes := pushHarness(t, fake, target)
	volumes.resolveRetryDelay = 0

	volumes.resolveRecovered()

	assert.Eventually(t, func() bool {
		return strings.Contains(lastRetag(fake), "--addtag beanstore.state=retired")
	}, 5*time.Second, 10*time.Millisecond)
}

func TestResolveRecoveredSkipsSettledStates(t *testing.T) {
	fake := &fakeRunner{outputs: []string{readyPushLV}}
	volumes := pushHarness(t, fake, &fakeTarget{})

	volumes.resolveRecovered()

	assert.Empty(t, lastRetag(fake), "no volume needed resolution")
}
