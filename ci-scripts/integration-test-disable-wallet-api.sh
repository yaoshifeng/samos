#!/bin/bash
# Runs "disable-wallet-api"-mode tests against a samos node configured with -enable-wallet-api=false
# "disable-wallet-api"-mode confirms that no wallet related apis work, that the main index.html page
# does not load, and that a new wallet file is not created.

#Set Script Name variable
SCRIPT=`basename ${BASH_SOURCE[0]}`
PORT="46421"
RPC_PORT="46431"
HOST="http://127.0.0.1:$PORT"
RPC_ADDR="127.0.0.1:$RPC_PORT"
MODE="disable-wallet-api"
BINARY="samos-integration"
TEST=""
UPDATE=""
# run go test with -v flag
VERBOSE=""
# run go test with -run flag
RUN_TESTS=""
FAILFAST=""

COMMIT=$(git rev-parse HEAD)
BRANCH=$(git rev-parse --abbrev-ref HEAD)
GOLDFLAGS="-X main.Commit=${COMMIT} -X main.Branch=${BRANCH}"

usage () {
  echo "Usage: $SCRIPT"
  echo "Optional command line arguments"
  echo "-t <string>  -- Test to run, gui or cli; empty runs both tests"
  echo "-r <string>  -- Run test with -run flag"
  echo "-u <boolean> -- Update stable testdata"
  echo "-v <boolean> -- Run test with -v flag"
  echo "-f <boolean> -- Run test with -failfast flag"
  exit 1
}

while getopts "h?t:r:uvf" args; do
  case $args in
    h|\?)
        usage;
        exit;;
    t ) TEST=${OPTARG};;
    r ) RUN_TESTS="-run ${OPTARG}";;
    u ) UPDATE="--update";;
    v ) VERBOSE="-v";;
    f ) FAILFAST="-failfast"
  esac
done

set -euxo pipefail

DATA_DIR=$(mktemp -d -t samos-data-dir.XXXXXX)
WALLET_DIR="${DATA_DIR}/wallets"

if [[ ! "$DATA_DIR" ]]; then
  echo "Could not create temp dir"
  exit 1
fi

# Compile the samos node
# We can't use "go run" because this creates two processes which doesn't allow us to kill it at the end
echo "compiling samos"
go build -o "$BINARY" -ldflags "${GOLDFLAGS}" cmd/samos/samos.go

# Run samos node with pinned blockchain database
echo "starting samos node in background with http listener on $HOST"

./samos-integration -disable-networking=true \
                      -web-interface-port=$PORT \
                      -download-peerlist=false \
                      -db-path=./src/gui/integration/test-fixtures/blockchain-180.db \
                      -db-read-only=true \
                      -rpc-interface=true \
                      -rpc-interface-port=$RPC_PORT \
                      -launch-browser=false \
                      -data-dir="$DATA_DIR" \
                      -wallet-dir="$WALLET_DIR" \
                      -enable-wallet-api=false &
SAMOS_PID=$!

echo "samos node pid=$SAMOS_PID"

echo "sleeping for startup"
sleep 3
echo "done sleeping"

set +e

if [[ -z $TEST || $TEST = "gui" ]]; then

SAMOS_INTEGRATION_TESTS=1 SAMOS_INTEGRATION_TEST_MODE=$MODE SAMOS_NODE_HOST=$HOST WALLET_DIR=$WALLET_DIR \
    go test ./src/gui/integration/... $FAILFAST $UPDATE -timeout=30s $VERBOSE $RUN_TESTS

GUI_FAIL=$?

fi

if [[ -z $TEST  || $TEST = "cli" ]]; then

SAMOS_INTEGRATION_TESTS=1 SAMOS_INTEGRATION_TEST_MODE=$MODE RPC_ADDR=$RPC_ADDR \
    go test ./src/api/cli/integration/... $FAILFAST $UPDATE -timeout=30s $VERBOSE $RUN_TESTS

CLI_FAIL=$?

fi


echo "shutting down samos node"

# Shutdown samos node
kill -s SIGINT $SAMOS_PID
wait $SAMOS_PID

rm "$BINARY"


if [[ (-z $TEST || $TEST = "gui") && $GUI_FAIL -ne 0 ]]; then
  exit $GUI_FAIL
elif [[ (-z $TEST || $TEST = "cli") && $CLI_FAIL -ne 0 ]]; then
  exit $CLI_FAIL
else
  exit 0
fi
# exit $FAIL
