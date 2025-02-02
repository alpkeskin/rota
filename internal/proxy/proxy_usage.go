package proxy

import "time"

func (ps *ProxyServer) updateProxyUsage(proxy *Proxy, reqInfo requestInfo, duration time.Duration, status string) {
	proxy.Lock()
	defer proxy.Unlock()

	proxy.LatestUsageStatus = status
	proxy.UsageCount++
	proxy.LatestUsageAt = time.Now().Format(time.RFC3339)
	proxy.LatestUsageDuration = duration.String()

	if proxy.UsageCount == 1 || proxy.AvgUsageDuration == "" {
		proxy.AvgUsageDuration = duration.String()
	} else {
		currentAvg, err := time.ParseDuration(proxy.AvgUsageDuration)
		if err == nil {
			newAvg := (currentAvg*time.Duration(proxy.UsageCount-1) + duration) / time.Duration(proxy.UsageCount)
			proxy.AvgUsageDuration = newAvg.String()
		} else {
			proxy.AvgUsageDuration = duration.String()
		}
	}

	history := ProxyHistory{
		Scheme:     proxy.Scheme,
		Host:       proxy.Host,
		Status:     status,
		Duration:   duration.String(),
		RequestUrl: reqInfo.url,
		UsedAt:     time.Now().Format(time.RFC3339),
	}

	ps.Lock()
	defer ps.Unlock()

	if len(ps.ProxyHistory) >= 1000 {
		newHistory := make([]ProxyHistory, len(ps.ProxyHistory))
		copy(newHistory, ps.ProxyHistory[1:])
		newHistory[len(newHistory)-1] = history
		ps.ProxyHistory = newHistory
	} else {
		ps.ProxyHistory = append(ps.ProxyHistory, history)
	}
}
