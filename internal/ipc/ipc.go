// Package ipc carries api frames between ytmd and its front-ends over a
// Unix socket, on every OS (Windows has AF_UNIX since 10 1803): the paths
// of the socket, lock, state and logs; the single-instance lock; listening,
// dialing, and starting ytmd when no daemon answers; and Conn, a frame
// stream whose writes never block the sender.
//
// The daemon starts so: TryLock (ErrLocked: one runs already, exit 0), load
// its state, then Listen, which removes a stale socket. A front-end dials
// through a Dialer, which starts ytmd whatever the failure: the lock makes
// a needless start harmless.
//
// Only the socket's directory (0700, the user's own) and the socket (0600)
// keep other users out; on Windows, %LOCALAPPDATA%'s ACL.
package ipc
