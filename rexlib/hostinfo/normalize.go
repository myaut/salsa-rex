package hostinfo

import "time"

func normalizeStatistic(v1, v2 int64, dt time.Duration) int64 {
	return (v2 - v1) * int64(time.Second) / int64(dt)
}

func (pi *HIProcInfo) normalizeStats(prevPi *HIProcInfo) {
	dt := pi.Lifetime - prevPi.Lifetime
	if dt < time.Microsecond {
		return
	}

	pi.RChar = normalizeStatistic(prevPi.rChar, pi.rChar, dt)
	pi.WChar = normalizeStatistic(prevPi.wChar, pi.wChar, dt)

	pi.VAllocs = normalizeStatistic(int64(prevPi.VSZ), int64(pi.VSZ), dt)
	pi.PAllocs = normalizeStatistic(int64(prevPi.RSS), int64(pi.RSS), dt)
}

func (ti *HIThreadInfo) normalizeStats(prevTi *HIThreadInfo) {
	dt := ti.Lifetime - prevTi.Lifetime
	if dt < time.Microsecond {
		return
	}

	ti.VCS = normalizeStatistic(prevTi.vcs, ti.vcs, dt)
	ti.IVCS = normalizeStatistic(prevTi.ivcs, ti.ivcs, dt)

	ti.MinFault = normalizeStatistic(prevTi.minFault, ti.minFault, dt)
	ti.MajFault = normalizeStatistic(prevTi.majFault, ti.majFault, dt)

	ti.STime = time.Duration(normalizeStatistic(int64(prevTi.sTime), int64(ti.sTime), dt))
	ti.UTime = time.Duration(normalizeStatistic(int64(prevTi.uTime), int64(ti.uTime), dt))
}
