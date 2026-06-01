package server

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"text/tabwriter"

	"github.com/weill-labs/amux/internal/mux"
)

type scrollbackDebugStats struct {
	SessionName  string
	DefaultLimit int
	Panes        []mux.PaneScrollbackStats
	Totals       scrollbackDebugTotals
}

type scrollbackDebugTotals struct {
	PaneCount      int
	BaseLines      int
	LiveLines      int
	TotalLines     int
	EffectiveLines int
	BaseBytes      uint64
	LiveBytes      uint64
	ScreenBytes    uint64
	EstimatedBytes uint64
}

func (s *Session) queryScrollbackDebugStatsContext(ctx context.Context) (scrollbackDebugStats, error) {
	return enqueueSessionQueryOnState(ctx, s, func(s *Session) (scrollbackDebugStats, error) {
		stats := scrollbackDebugStats{
			SessionName:  s.Name,
			DefaultLimit: s.scrollback.DefaultLines,
		}
		if stats.DefaultLimit <= 0 {
			stats.DefaultLimit = mux.DefaultScrollbackLines
		}
		panes := append([]*mux.Pane(nil), s.Panes...)
		slices.SortFunc(panes, func(a, b *mux.Pane) int {
			switch {
			case a == nil && b == nil:
				return 0
			case a == nil:
				return 1
			case b == nil:
				return -1
			case a.Meta.Host < b.Meta.Host:
				return -1
			case a.Meta.Host > b.Meta.Host:
				return 1
			case a.Meta.Name < b.Meta.Name:
				return -1
			case a.Meta.Name > b.Meta.Name:
				return 1
			case a.ID < b.ID:
				return -1
			case a.ID > b.ID:
				return 1
			default:
				return 0
			}
		})
		for _, pane := range panes {
			if pane == nil {
				continue
			}
			paneStats := pane.ScrollbackStats()
			stats.Panes = append(stats.Panes, paneStats)
			stats.Totals.add(paneStats)
		}
		return stats, nil
	})
}

func (t *scrollbackDebugTotals) add(stats mux.PaneScrollbackStats) {
	t.PaneCount++
	t.BaseLines += stats.BaseLines
	t.LiveLines += stats.LiveLines
	t.TotalLines += stats.TotalLines
	t.EffectiveLines += stats.EffectiveLines
	t.BaseBytes += stats.BaseBytes
	t.LiveBytes += stats.LiveBytes
	t.ScreenBytes += stats.ScreenBytes
	t.EstimatedBytes += stats.EstimatedBytes
}

func formatDebugScrollback(stats scrollbackDebugStats) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Scrollback memory estimate for session %s\n", stats.SessionName)
	fmt.Fprintf(&buf, "Default limit: %d lines\n\n", stats.DefaultLimit)

	tw := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PANE\tHOST\tSIZE\tLIMIT\tBASE\tLIVE\tRESIDENT\tEFFECTIVE\tESTIMATED")
	for _, pane := range stats.Panes {
		fmt.Fprintf(tw, "%s\t%s\t%dx%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
			pane.PaneName,
			pane.Host,
			pane.Width,
			pane.Height,
			pane.LimitLines,
			pane.BaseLines,
			pane.LiveLines,
			pane.TotalLines,
			pane.EffectiveLines,
			formatDebugBytes(pane.EstimatedBytes),
		)
	}
	_ = tw.Flush()

	totals := stats.Totals
	fmt.Fprintf(&buf, "\nTotals: panes=%d base=%d live=%d resident=%d effective=%d estimated=%s\n",
		totals.PaneCount,
		totals.BaseLines,
		totals.LiveLines,
		totals.TotalLines,
		totals.EffectiveLines,
		formatDebugBytes(totals.EstimatedBytes),
	)
	fmt.Fprintf(&buf, "Breakdown: base=%s live=%s screen=%s\n",
		formatDebugBytes(totals.BaseBytes),
		formatDebugBytes(totals.LiveBytes),
		formatDebugBytes(totals.ScreenBytes),
	)
	buf.WriteString("Estimate includes base history strings, VT live scrollback cells, and main/alternate screen buffers.\n")
	return buf.String()
}

func formatDebugBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	for _, suffix := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f PiB", value/unit)
}
