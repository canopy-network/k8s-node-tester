package main

import (
	"log/slog"
	"time"
)

// HeightCh represents a channel for height updates
type HeightCh struct {
	Height  uint64 `json:"height"`  // actual height of the block
	Counter uint64 `json:"counter"` // height counter of the block for incremental mode
}

type HeightResp struct {
	Height int `json:"height"`
}

type newBlockNotifier struct {
	log           *slog.Logger
	config        General
	checkInterval time.Duration
	maxRetries    int

	heightCh    chan HeightCh
	lastHeight  uint64
	retries     int
	initialized bool
	counter     uint64
}

// newNotifier creates a new block notifier
func newNotifier(log *slog.Logger, config General, checkInterval time.Duration, maxRetries int) *newBlockNotifier {
	return &newBlockNotifier{
		log:           log,
		config:        config,
		checkInterval: checkInterval,
		maxRetries:    maxRetries,
		heightCh:      make(chan HeightCh),
		lastHeight:    uint64(0),
		retries:       0,
		initialized:   !config.WaitForNewBlock,
		counter:       0,
	}
}

// handleHeight handles the height of a new block depending on the profile settings
func (n *newBlockNotifier) handleHeight(height uint64) (stop bool, h uint64, counter uint64) {
	// emit actual chain height until it exceeds MaxHeight
	max := n.config.MaxHeight
	if !n.config.Incremental {
		if height <= max {
			return false, height, 0
		}
		return true, height, 0
	}
	// incremental mode: height becomes a 0 based counter, incremented by 1 per block
	n.counter++
	if n.counter <= max {
		return false, height, n.counter
	}
	return true, height, n.counter
}

// run starts the block notifier
func (n *newBlockNotifier) run() {
	defer close(n.heightCh)
	for range time.Tick(n.checkInterval) {
		resp, err := cnpyClient.Height()
		if err != nil {
			n.log.Error("get block height failed",
				slog.String("err", err.Error()),
				slog.Int("retry", n.retries),
				slog.Int("maxRetries", n.maxRetries),
			)
			n.retries++
			if n.retries > n.maxRetries {
				return
			}
			continue
		}
		// reset retries on success
		n.retries = 0
		// ignore genesis or non-increasing heights
		if resp.Height == 0 || resp.Height <= n.lastHeight {
			continue
		}
		// sleep for notifyDelay before emitting the height
		notifyDelay := time.Duration(n.config.NotifyNewBlockDelayMs) * time.Millisecond
		if notifyDelay > 0 {
			time.Sleep(notifyDelay)
		}
		n.lastHeight = resp.Height
		// wait for the next block on the very first iteration so is always notified on a "new block"
		if !n.initialized {
			n.initialized = true
			continue
		}
		// handle the new height
		stop, height, counter := n.handleHeight(resp.Height)
		if stop {
			return
		}
		n.heightCh <- HeightCh{
			Height:  height,
			Counter: counter,
		}
	}
}

// BlockNotifier creates a new block notifier that emits the height of every new block
func BlockNotifier(log *slog.Logger, config General, timeout time.Duration,
	checkInterval time.Duration, maxRetries int) <-chan HeightCh {
	n := newNotifier(log, config, checkInterval, maxRetries)
	go n.run()
	return n.heightCh
}
