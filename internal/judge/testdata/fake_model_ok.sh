#!/bin/sh
# Fake model for tests: ignores stdin, always answers apply.
cat > /dev/null
echo '{"verdict": "apply", "blockers": [], "reason": "fake model says yes"}'
