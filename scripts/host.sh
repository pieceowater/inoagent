#!/bin/sh
# Prints user@host of the first server under [inoagent] in the inventory, for
# the Makefile's remote targets (logs, shell, status…).
awk '/^\[inoagent\]/ {f=1; next} /^\[/ {f=0}
     f && NF && $1 !~ /^[#;]/ {
       u = ""
       for (i = 2; i <= NF; i++) if ($i ~ /^ansible_user=/) { split($i, a, "="); u = a[2] "@" }
       print u $1; exit
     }' "$(dirname "$0")/../ansible/inventory/hosts.ini" 2>/dev/null
