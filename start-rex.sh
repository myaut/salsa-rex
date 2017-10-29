#!/bin/bash

#
# Starts rex infrastructure: monitoring daemon, trace daemon & cli
# TODO: start on remote hosts?
#

function get_ini_value() {
	awk -F= "/^$2/ { print \$2 ; exit }" $1 | tr -d ' '
}

GOBIN=${GOBIN:-${GOPATH}/bin}
REX=$GOBIN/rex 

TRACECFG=rex.ini
TRACESOCK=`get_ini_value $TRACECFG Socket`
TRACEPID=
[ "$TRACESOCK" ] || exit 1

MONCFG=rex-mon.ini
MONSOCK=`get_ini_value $MONCFG Socket`
MONPID=
[ "$MONSOCK" ] || exit 1

function prepare_dirs() {
	local dir
	dir=`get_ini_value $1 DataDir`
	
	if ! [ -d $dir ] ; then 
		mkdir -p $dir
		echo "Created directory $dir"
	fi
}

function kill_daemons() {
	echo "Killing daemons with PIDs $MONPID & $TRACEPID"
	kill $MONPID
	kill $TRACEPID
	wait $MONPID $TRACEPID
}

trap "kill_daemons" EXIT

# Start servers 
if [ -f $TRACESOCK ] ; then
	echo "Rex tracing daemon has been started already"
else 
	prepare_dirs $TRACECFG 
 	$REX -t -config $TRACECFG &
	TRACEPID=$!
fi

if [ -f $MONSOCK ] ; then
	echo "Rex monitoring daemon has been started already"
else 
	prepare_dirs $MONCFG
 	$REX -mon -config $MONCFG &
	MONPID=$!
fi

# Wait for monitoring daemon to start and start accepting connections
while ! [ -S $MONSOCK ] ; do 
	sleep 0.1
done

$REX -config $MONCFG "$@"

