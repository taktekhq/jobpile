#!/bin/sh
# Fake model for tests: ignores stdin, answers with invalid JSON every time.
cat > /dev/null
echo 'not json at all'
