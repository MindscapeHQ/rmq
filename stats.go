package rmq

import (
	"bytes"
	"fmt"
	"sort"
)

type ConnectionStat struct {
	active       bool
	UnackedCount int
	consumers    []string
}

func (stat ConnectionStat) String() string {
	return fmt.Sprintf("[unacked:%d consumers:%d]",
		stat.UnackedCount,
		len(stat.consumers),
	)
}

type ConnectionStats map[string]ConnectionStat

type QueueStat struct {
	ReadyCount      int `json:"ready"`
	RejectedCount   int `json:"rejected"`
	ConnectionStats ConnectionStats
}

func NewQueueStat(readyCount, rejectedCount int) QueueStat {
	return QueueStat{
		ReadyCount:      readyCount,
		RejectedCount:   rejectedCount,
		ConnectionStats: ConnectionStats{},
	}
}

func (stat QueueStat) String() string {
	return fmt.Sprintf("[ready:%d rejected:%d conn:%s",
		stat.ReadyCount,
		stat.RejectedCount,
		stat.ConnectionStats,
	)
}

func (stat QueueStat) unackedCount() int {
	unacked := 0
	for _, connectionStat := range stat.ConnectionStats {
		unacked += connectionStat.UnackedCount
	}
	return unacked
}

func (stat QueueStat) ConsumerCount() int {
	consumer := 0
	for _, connectionStat := range stat.ConnectionStats {
		consumer += len(connectionStat.consumers)
	}
	return consumer
}

type QueueStats map[string]QueueStat

type Stats struct {
	QueueStats       QueueStats      `json:"queues"`
	otherConnections map[string]bool // non consuming connections, active or not
}

func NewStats() Stats {
	return Stats{
		QueueStats:       QueueStats{},
		otherConnections: map[string]bool{},
	}
}

func CollectStats(queueList []string, mainConnection *redisConnection) Stats {
	stats := NewStats()
	for _, queueName := range queueList {
		queue := mainConnection.openQueue(queueName)
		stats.QueueStats[queueName] = NewQueueStat(queue.ReadyCount(), queue.RejectedCount())
	}

	connectionNames := mainConnection.GetConnections()
	for _, connectionName := range connectionNames {
		connection := mainConnection.hijackConnection(connectionName)
		connectionActive := connection.Check()

		queueNames := connection.GetConsumingQueues()
		if len(queueNames) == 0 {
			stats.otherConnections[connectionName] = connectionActive
			continue
		}

		for _, queueName := range queueNames {
			queue := connection.openQueue(queueName)
			consumers := queue.GetConsumers()
			openQueueStat, ok := stats.QueueStats[queueName]
			if !ok {
				continue
			}
			openQueueStat.ConnectionStats[connectionName] = ConnectionStat{
				active:       connectionActive,
				UnackedCount: queue.UnackedCount(),
				consumers:    consumers,
			}
		}
	}

	return stats
}

func (stats Stats) String() string {
	var buffer bytes.Buffer

	for queueName, queueStat := range stats.QueueStats {
		buffer.WriteString(fmt.Sprintf("    queue:%s ready:%d rejected:%d unacked:%d consumers:%d\n",
			queueName, queueStat.ReadyCount, queueStat.RejectedCount, queueStat.unackedCount(), queueStat.ConsumerCount(),
		))

		for connectionName, connectionStat := range queueStat.ConnectionStats {
			buffer.WriteString(fmt.Sprintf("        connection:%s unacked:%d consumers:%d active:%t\n",
				connectionName, connectionStat.UnackedCount, len(connectionStat.consumers), connectionStat.active,
			))
		}
	}

	for connectionName, active := range stats.otherConnections {
		buffer.WriteString(fmt.Sprintf("    connection:%s active:%t\n",
			connectionName, active,
		))
	}

	return buffer.String()
}

func (stats Stats) GetHtml(layout, refresh string) string {
	buffer := bytes.NewBufferString("<html>")

	if refresh != "" {
		buffer.WriteString(fmt.Sprintf(`<head><meta http-equiv="refresh" content="%s">`, refresh))
	}

	buffer.WriteString(`<body><table style="font-family:monospace">`)
	buffer.WriteString(`<tr><td>` +
		`queue</td><td></td><td>` +
		`ready</td><td></td><td>` +
		`rejected</td><td></td><td>` +
		`</td><td></td><td>` +
		`connections</td><td></td><td>` +
		`unacked</td><td></td><td>` +
		`consumers</td><td></td></tr>`,
	)

	for _, queueName := range stats.sortedQueueNames() {
		queueStat := stats.QueueStats[queueName]
		connectionNames := queueStat.ConnectionStats.sortedNames()
		buffer.WriteString(fmt.Sprintf(`<tr><td>`+
			`%s</td><td></td><td>`+
			`%d</td><td></td><td>`+
			`%d</td><td></td><td>`+
			`%s</td><td></td><td>`+
			`%d</td><td></td><td>`+
			`%d</td><td></td><td>`+
			`%d</td><td></td></tr>`,
			queueName, queueStat.ReadyCount, queueStat.RejectedCount, "", len(connectionNames), queueStat.unackedCount(), queueStat.ConsumerCount(),
		))

		if layout != "condensed" {
			for _, connectionName := range connectionNames {
				connectionStat := queueStat.ConnectionStats[connectionName]
				buffer.WriteString(fmt.Sprintf(`<tr style="color:lightgrey"><td>`+
					`%s</td><td></td><td>`+
					`%s</td><td></td><td>`+
					`%s</td><td></td><td>`+
					`%s</td><td></td><td>`+
					`%s</td><td></td><td>`+
					`%d</td><td></td><td>`+
					`%d</td><td></td></tr>`,
					"", "", "", ActiveSign(connectionStat.active), connectionName, connectionStat.UnackedCount, len(connectionStat.consumers),
				))
			}
		}
	}

	if layout != "condensed" {
		buffer.WriteString(`<tr><td>-----</td></tr>`)
		for _, connectionName := range stats.sortedConnectionNames() {
			active := stats.otherConnections[connectionName]
			buffer.WriteString(fmt.Sprintf(`<tr style="color:lightgrey"><td>`+
				`%s</td><td></td><td>`+
				`%s</td><td></td><td>`+
				`%s</td><td></td><td>`+
				`%s</td><td></td><td>`+
				`%s</td><td></td><td>`+
				`%s</td><td></td><td>`+
				`%s</td><td></td></tr>`,
				"", "", "", ActiveSign(active), connectionName, "", "",
			))
		}
	}

	buffer.WriteString(`</table></body></html>`)
	return buffer.String()
}

func (stats ConnectionStats) sortedNames() []string {
	var keys []string
	for key := range stats {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (stats Stats) sortedQueueNames() []string {
	var keys []string
	for key := range stats.QueueStats {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (stats Stats) sortedConnectionNames() []string {
	var keys []string
	for key := range stats.otherConnections {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func ActiveSign(active bool) string {
	if active {
		return "✓"
	}
	return "✗"
}
