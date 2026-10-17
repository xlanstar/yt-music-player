// Package ipc carries api frames between ytmd and its front-ends over a
// Unix socket on every OS (Windows 10 1803+): ytmd's paths, its
// single-instance lock, listening, dialing and starting ytmd, and Conn,
// whose sends never block.
//
// ytmd takes the lock before it listens, removing a stale socket. A Dialer
// starts ytmd on any failure to dial: the lock makes a needless start
// harmless. Only the socket's directory (0700) and the socket (0600) keep
// other users out; on Windows, %LOCALAPPDATA%'s ACL.
package ipc
