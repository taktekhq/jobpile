#!/bin/sh
# Fake notify command for tests: always exits non-zero, simulating a
# broken notify_command.
cat > /dev/null
echo 'boom' >&2
exit 1
