#!/bin/sh
# Fake model for tests: always exits non-zero, simulating a broken model command.
cat > /dev/null
echo 'boom' >&2
exit 1
