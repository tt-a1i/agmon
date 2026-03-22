package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tt-a1i/agmon/internal/event"
)

func subscriberSocketPath(sockPath string) string {
	ext := filepath.Ext(sockPath)
	if ext == "" {
		return sockPath + ".events"
	}
	base := strings.TrimSuffix(sockPath, ext)
	return base + ".events" + ext
}

func SubscribeRemote(sockPath string) (<-chan event.Event, func(), error) {
	conn, err := dialSocket(subscriberSocketPath(sockPath))
	if err != nil {
		return nil, nil, err
	}

	eventCh := make(chan event.Event, 256)
	var once sync.Once
	closeFn := func() {
		once.Do(func() {
			conn.Close()
		})
	}

	go func() {
		defer close(eventCh)
		defer closeFn()

		dec := json.NewDecoder(conn)
		for {
			var ev event.Event
			if err := dec.Decode(&ev); err != nil {
				return
			}
			eventCh <- ev
		}
	}()

	return eventCh, closeFn, nil
}
