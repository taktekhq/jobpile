#!/bin/sh
# Fake notify command for tests: writes stdin to the file named in $1,
# then exits 0.
cat > "$1"
