# WP13 allocation recovery

**Issue:** Automatic dependency-lane allocation could not merge WP11 because
tracked derived `status.json` conflicted under sparse checkout.

**Resolution:** The approved WP11 lane was merged manually into lane-m at
`6488469e`. The only unresolved path was derived mission status; resolution
preserved the coordination branch's current status while retaining every WP11
product commit. The lane is clean and the original blocking condition no
longer exists.
