package main

func (m *model) selectQueueTrack(index int) {
	if m.queue.Select(index) {
		m.playCurrent()
		m.saveQueue()
	}
}

func (m *model) removeQueueTrack(index int) {
	if index < 0 || index >= len(m.queue.Tracks) {
		return
	}
	if m.queue.Remove(index) {
		m.cancelPlayback()
		if m.queue.Current() != nil {
			m.playCurrent()
		}
	}
	m.saveQueue()
}

func (m *model) clearQueue() {
	m.cancelPlayback()
	m.queue.Replace(nil, 0)
	m.audioState.Position = 0
	m.audioState.Duration = 0
	m.saveQueue()
	m.notify("队列已清空")
}
