package projection

import (
	"sync"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type NotificationV2 struct {
	DatabaseID       journalv2.DatabaseID     `json:"database_id"`
	Revision         uint64                   `json:"revision"`
	RuntimeEpochID   journalv2.RuntimeEpochID `json:"runtime_epoch_id"`
	ProjectionDigest string                   `json:"projection_digest_sha256"`
	ResyncRequired   bool                     `json:"resync_required"`
}
type WatchV2 struct {
	C      <-chan NotificationV2
	cancel func()
	once   sync.Once
}

func (w *WatchV2) Cancel() {
	if w != nil {
		w.once.Do(func() {
			if w.cancel != nil {
				w.cancel()
			}
		})
	}
}

type watchStateV2 struct {
	channel chan NotificationV2
	epoch   journalv2.RuntimeEpochID
}

func (a *AuthorityV2) Watch(databaseID journalv2.DatabaseID, afterRevision uint64) (*WatchV2, error) {
	if a == nil {
		return nil, &ErrorV2{ErrorInvalidQueryV2, "watch"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	snapshot := a.current.Load()
	if snapshot == nil || databaseID != snapshot.databaseID || afterRevision != snapshot.revision || len(a.watches) >= MaxProjectionWatchesV2 {
		return nil, &ErrorV2{ErrorResyncRequiredV2, "watch_start"}
	}
	a.nextWatch++
	id := a.nextWatch
	channel := make(chan NotificationV2, 1)
	a.watches[id] = &watchStateV2{channel, snapshot.epoch}
	return &WatchV2{C: channel, cancel: func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if state, ok := a.watches[id]; ok {
			delete(a.watches, id)
			close(state.channel)
		}
	}}, nil
}

func (a *AuthorityV2) notifyLocked(snapshot *SnapshotV2, epochChanged bool) {
	for id, watch := range a.watches {
		notification := NotificationV2{snapshot.databaseID, snapshot.revision, snapshot.epoch, snapshot.projectionDigest.String(), epochChanged || watch.epoch != snapshot.epoch}
		select {
		case watch.channel <- notification:
			watch.epoch = snapshot.epoch
			if notification.ResyncRequired {
				close(watch.channel)
				delete(a.watches, id)
			}
		default:
			select {
			case <-watch.channel:
			default:
			}
			notification.ResyncRequired = true
			watch.channel <- notification
			close(watch.channel)
			delete(a.watches, id)
		}
	}
}
