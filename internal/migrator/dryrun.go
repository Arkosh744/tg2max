package migrator

import (
	"github.com/arkosh/tg2max/internal/converter"
	"github.com/arkosh/tg2max/pkg/models"
)

// DryRunStats holds statistics about what would happen during migration
// without actually sending any messages.
type DryRunStats struct {
	TotalInput     int
	OutputMessages int // after grouping and splitting
	GroupedCount   int // messages merged into groups
	SplitCount     int // messages split due to length
	MediaCount     int
	StickerCount   int
	TextOnlyCount  int
}

// DryRun performs a full pass through messages without sending anything,
// returning statistics about what would happen during an actual migration.
func DryRun(messages []models.Message, conv *converter.Converter) DryRunStats {
	var stats DryRunStats
	stats.TotalInput = len(messages)

	i := 0
	for i < len(messages) {
		// Build consecutive group from same author within time window
		group := []models.Message{messages[i]}
		for j := i + 1; j < len(messages); j++ {
			if !converter.CanGroup(group[len(group)-1], messages[j]) {
				break
			}
			group = append(group, messages[j])
		}

		if len(group) > 1 {
			stats.GroupedCount += len(group)
			text := conv.FormatGroupForMax(group, "")
			chunks := conv.SplitMessage(text, converter.MaxMessageLength)
			stats.OutputMessages += len(chunks)
			if len(chunks) > 1 {
				stats.SplitCount++
			}
			stats.TextOnlyCount++
		} else {
			msg := group[0]
			text := conv.FormatForMax(msg, "")
			chunks := conv.SplitMessage(text, converter.MaxMessageLength)
			stats.OutputMessages += len(chunks)
			if len(chunks) > 1 {
				stats.SplitCount++
			}
			if msg.StickerEmoji != "" {
				stats.StickerCount++
				stats.TextOnlyCount++
			} else if len(msg.Media) > 0 {
				stats.MediaCount++
				// First media item is sent together with text; each additional media is a separate request
				stats.OutputMessages += len(msg.Media) - 1
			} else {
				stats.TextOnlyCount++
			}
		}

		i += len(group)
	}

	return stats
}
