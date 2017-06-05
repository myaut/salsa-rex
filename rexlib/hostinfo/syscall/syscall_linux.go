package syscall

/*
#include <unistd.h>
#include <sys/types.h>
*/
import "C"

/**
 * Returns number of jiffies per second
 */
func GetClkTck() (jiffiesPerSecond uint64) {
	var sc_clk_tck C.long
	sc_clk_tck = C.sysconf(C._SC_CLK_TCK)
	jiffiesPerSecond = uint64(sc_clk_tck)
	return
}
