# busy_wait_1 -- a simplest scenario which involves monitoring of cpu usage
# by tsexperiment and busy_wait experiment itself

create -o busy_wait_1 {
	tsload {
		threadpool -nt 2 
		workload bw busy_wait {
			param -i -rg lcg -rv uniform -min 2000000 -max 4000000 num_cycles
			steps 10 12 14 16 18 20 25 30 24 19 15 10
		}
	}
	
	add -commit sysstat cpu_usr
	start
	stop -wait
}

exit 
