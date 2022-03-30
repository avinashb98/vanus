// Copyright 2022 Linkall Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package trigger

import (
	"context"
	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/linkall-labs/vanus/internal/primitive"
	"github.com/linkall-labs/vanus/internal/trigger/filter"
	"github.com/linkall-labs/vanus/internal/trigger/info"
	"github.com/linkall-labs/vanus/internal/trigger/offset"
	"github.com/linkall-labs/vanus/internal/util"
	"github.com/linkall-labs/vanus/observability/log"
	"sync"
	"time"
)

type TriggerState string

const (
	TriggerCreated   = "created"
	TriggerPending   = "pending"
	TriggerRunning   = "running"
	TriggerSleep     = "sleep"
	TriggerPaused    = "paused"
	TriggerStopped   = "stopped"
	TriggerDestroyed = "destroyed"
)

type Trigger struct {
	ID             string        `json:"id"`
	SubscriptionID string        `json:"subscription_id"`
	Target         primitive.URI `json:"target"`
	SleepDuration  time.Duration `json:"sleep_duration"`

	state      TriggerState
	stateMutex sync.RWMutex
	lastActive time.Time

	offsetManager *offset.SubscriptionOffset
	stop          context.CancelFunc
	eventCh       chan info.EventRecord
	sendCh        chan info.EventRecord
	ceClient      ce.Client
	filter        filter.Filter
	config        Config

	wg util.Group
}

func NewTrigger(config *Config, sub *primitive.Subscription, offsetManager *offset.SubscriptionOffset) *Trigger {
	if config == nil {
		config = &Config{}
	}
	config.initConfig()
	return &Trigger{
		config:         *config,
		ID:             uuid.New().String(),
		SubscriptionID: sub.ID,
		Target:         sub.Sink,
		SleepDuration:  30 * time.Second,
		state:          TriggerCreated,
		filter:         filter.GetFilter(sub.Filters),
		eventCh:        make(chan info.EventRecord, config.BufferSize),
		sendCh:         make(chan info.EventRecord, config.BufferSize),
		offsetManager:  offsetManager,
	}
}

func (t *Trigger) EventArrived(ctx context.Context, event info.EventRecord) error {
	select {
	case t.eventCh <- event:
		t.offsetManager.EventReceive(event.OffsetInfo)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Trigger) retrySendEvent(ctx context.Context, e *ce.Event) error {
	retryTimes := 0
	doFunc := func() error {
		timeout, cancel := context.WithTimeout(ctx, t.config.SendTimeOut)
		defer cancel()
		return t.ceClient.Send(timeout, *e)
	}
	var err error
	for retryTimes < t.config.MaxRetryTimes {
		retryTimes++
		if err = doFunc(); !ce.IsACK(err) {
			log.Debug(ctx, "process event error", map[string]interface{}{
				"error": err, "retryTimes": retryTimes,
			})
			time.Sleep(t.config.RetryPeriod)
		} else {
			log.Debug(ctx, "send ce event success", map[string]interface{}{
				"event": e,
			})
			return nil
		}
	}
	return err
}

func (t *Trigger) runEventProcess(ctx context.Context) {
	for {
		select {
		//TODO  是否立即停止，还是等待eventCh处理完
		case <-ctx.Done():
			return
		case event, ok := <-t.eventCh:
			if !ok {
				return
			}
			if res := filter.FilterEvent(t.filter, *event.Event); res == filter.FailFilter {
				t.offsetManager.EventCommit(event.OffsetInfo)
				continue
			}
			t.sendCh <- event
		}
	}
}

func (t *Trigger) runEventSend(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-t.sendCh:
			if !ok {
				return
			}
			err := t.retrySendEvent(ctx, event.Event)
			if err != nil {
				log.Error(ctx, "send event to sink failed", map[string]interface{}{
					log.KeyError: err,
					"event":      event,
				})
			}
			t.offsetManager.EventCommit(event.OffsetInfo)
		}
	}
}

func (t *Trigger) runSleepWatch(ctx context.Context) {
	tk := time.NewTicker(10 * time.Millisecond)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			t.stateMutex.Lock()
			if t.state == TriggerRunning {
				if time.Now().Sub(t.lastActive) > t.SleepDuration {
					t.state = TriggerSleep
				} else {
					t.state = TriggerRunning
				}
			}
			t.stateMutex.Unlock()
		}
	}
}

func (t *Trigger) Start() error {
	ceClient, err := primitive.NewCeClient(t.Target)
	if err != nil {
		return err
	}
	t.ceClient = ceClient
	ctx, cancel := context.WithCancel(context.Background())
	t.stop = cancel
	for i := 0; i < t.config.FilterProcessSize; i++ {
		t.wg.StartWithContext(ctx, t.runEventProcess)
	}
	for i := 0; i < t.config.SendProcessSize; i++ {
		t.wg.StartWithContext(ctx, t.runEventSend)
	}
	t.wg.StartWithContext(ctx, t.runSleepWatch)

	t.state = TriggerRunning
	t.lastActive = time.Now()
	return nil
}

func (t *Trigger) Stop() {
	ctx := context.Background()
	log.Info(ctx, "trigger stop...", map[string]interface{}{
		"subId": t.SubscriptionID,
	})
	if t.state == TriggerStopped {
		return
	}
	t.stop()
	t.wg.Wait()
	close(t.eventCh)
	close(t.sendCh)
	t.state = TriggerStopped
	log.Info(ctx, "trigger stopped", map[string]interface{}{
		"subId": t.SubscriptionID,
	})
}

func (t *Trigger) GetState() TriggerState {
	t.stateMutex.RLock()
	defer t.stateMutex.RUnlock()
	return t.state
}