package app

import "github.com/Mag1cFall/AIStudio2API/internal/aistudio"

// upstreamChannels converts configured channel names to pool channels
func upstreamChannels(names []string) []aistudio.Channel {
	channels := make([]aistudio.Channel, 0, len(names))
	for _, name := range names {
		channels = append(channels, aistudio.Channel(name))
	}
	return channels
}
