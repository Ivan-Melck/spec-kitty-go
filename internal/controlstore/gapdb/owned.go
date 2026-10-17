package gapdb

import (
	"context"
	"sync"
	"io"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
)

// DialOwned is an internal infrastructure constructor, NOT admission or a Host
// authority factory. Production only reaches it via the genuine Lifecycle's
// private retained state and originally authenticated bootstrap status. Supplying
// identity constraints here cannot mint a production owner or census proof.
// The returned closer owns SDK/connection resources, never Lifetime descriptors.
func DialOwned(ctx context.Context, endpoint string, timeout time.Duration, lifetime *retainedkernel.Lifetime, database string, ownerPID int, ownerStarted string) (*Adapter, io.Closer, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if lifetime == nil || endpoint == "" || timeout <= 0 || database == "" || ownerPID <= 0 || ownerStarted == "" {
		return nil, nil, adapterError("dial_owned", "", OutcomeInvalid, RecoveryFixRequest, "owned identity constraints are required", nil)
	}
	transport := &retainedTransport{lifetime: lifetime, endpoint: endpoint, database: database, ownerPID: ownerPID, ownerStarted: ownerStarted, timeout: timeout}
	status, err := transport.status(ctx)
	if err != nil {
		return nil, nil, withOwnedCustody(translateError("dial_owned", "", err), transport.Close())
	}
	if status.Lifecycle != gapdbapi.LifecycleReady {
		return nil, nil, withOwnedCustody(adapterError("dial_owned", "", OutcomeCapabilityAbsent, RecoveryOperator, "owned daemon is not ready", nil), transport.Close())
	}
	client, err := gapdbapi.Dial(endpoint, gapdbapi.ClientOptions{Timeout: timeout, ReuseUnaryConnection: true})
	if err != nil {
		return nil, nil, withOwnedCustody(translateError("dial_owned", "", err), transport.Close())
	}
	adapter, err := newAdapter(&ownedProvider{provider: &publicProvider{client: client}, transport: transport}, endpoint, timeout)
	if err != nil {
		transportErr := transport.Close()
		clientErr := client.Close() // established transport -> SDK ordering
		return nil, nil, withOwnedCustody(err, withOwnedCustody(transportErr, clientErr))
	}
	adapter.lifetime = lifetime
	return adapter, &ownedCloser{sdk: client, transport: transport}, nil
}

type ownedCloser struct {
	once sync.Once
	err error
	sdk       io.Closer
	transport *retainedTransport
}

func (c *ownedCloser) Close() error {
	c.once.Do(func() {
		transportErr := c.transport.Close()
		sdkErr := c.sdk.Close()
		c.err = withOwnedCustody(transportErr, sdkErr)
	})
	return c.err
}
