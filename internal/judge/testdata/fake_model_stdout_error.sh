#!/bin/sh
# Fake model for tests: reproduces claude-ledger's text mode, which
# writes the model's own error message to stdout (not stderr) and
# still exits non-zero. This is the 12 Oct live-run failure mode:
# "Prompt is too long ..." printed to stdout, exit 1, empty stderr.
cat > /dev/null
echo 'Prompt is too long - the request is ~235657 tokens (limit 200000)'
exit 1
