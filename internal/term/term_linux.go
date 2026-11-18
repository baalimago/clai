package term

import "syscall"

const ioctlGetTermios = syscall.TCGETS

// ioctlSetTermiosFlush is TCSETSF, which syscall does not export; it follows
// TCSETS by two on every Linux architecture.
const ioctlSetTermiosFlush = syscall.TCSETS + 2
