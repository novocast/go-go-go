# Speed-Test Ideas

This document collects candidate workloads for Go Go Go, a terminal race for comparing implementations that perform the same work. Each idea should use fixed, reproducible input so that lane results are comparable.

## Ideas

4. Sort a large dataset in parallel, then merge the sorted chunks.