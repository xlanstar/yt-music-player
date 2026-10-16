# ytmd / ytm：規格與實作計劃

狀態：下一步是 P2（§11）；風險實驗的結論見 §12。全部完成後，本文件濃縮成英文的 `docs/daemon.md`（協定與路徑），其餘刪除；`AGENTS.md` 的 Layout 與 Commands 同步更新。

## 1. 目標與範圍

- 播放移到背景常駐程式 `ytmd`；Desktop（MyGo）與 `ytm`（TUI + CLI）只負責控制與瀏覽。
- 關閉任何前端都不停止播放；daemon 需要時才啟動，開機自動啟動是選項，預設關閉。
- macOS、Linux、Windows 都發布（amd64、arm64；macOS 為 universal）。
- 登入方式：從 Desktop，或用 `ytm login` 從 stdin 讀入 cookie（沒有桌面環境的 Linux 也能用）。
- Desktop 和 TUI 都會檢查 daemon 是否還活著、版本是否太舊，並自動處理（§4.3）。

不在第一版範圍內：

- TUI 顯示圖片。
- Windows 媒體鍵（SMTC），和現況相同。
- 遠端或網路控制：只走本機 socket。
- 舊 Keychain 項目遷移：Keychain 的存取權依程式簽章而定，macOS 升級後 ytmd 讀不到 Desktop 存的項目，要重新登入一次。Linux 和 Windows 的帳密是 `<cfg>` 下的檔案，ytmd 讀得到，不受影響。

## 2. 執行檔與目錄

```
yt-music-player/                      一個 module
├── resources/                        MyGo 打包的資源；輔助執行檔建置到 resources/<platform>/（§9.1）
├── cmd/
│   ├── desktop/                      MyGo Desktop（現有）
│   ├── ytmd/                         daemon
│   └── ytm/                          TUI + CLI
├── locales/                          維持在根目錄，三個執行檔共用
└── internal/
    ├── api/        新增：協定型別（Frame、方法、事件、錯誤碼、Proto），只放型別與純函式
    ├── ipc/        新增：路徑、listen/dial、單一實例鎖、spawn、gob 連線、寫入佇列
    ├── daemon/     新增：Service（event loop 持有狀態）、請求分派、訂閱、持久化
    ├── remote/     新增：前端的 client，實作 catalog.Source、catalog.Accounts、Controller；Supervisor
    ├── loop/       新增：單一 goroutine 的執行佇列，給 daemon 的 post 用
    ├── version/    新增：Version（-ldflags -X 注入，預設 "dev"）
    ├── logfile/    新增：固定大小的輪替 log 寫入器
    ├── autostart/  新增：LaunchAgent、systemd user unit、HKCU Run；Windows 的 `Path` 開關（§9.3）
    ├── tui/        新增：Bubble Tea v2 的畫面
    └── （現有套件；`account` 歸 daemon，`session`、`noticetext` 歸前端，見 §6.1、§5.6）
```

### 依賴規則

由每個 `cmd/*` 的 `TestDeps` 以 `go list -deps` 檢查執行檔本身；測試二進位不受限制（同一行程內的 Service 會連結 `audio.Fake`）。`api`、`ipc`、`catalog`、`job` 這類共用套件三者都可以用，下面不列出。

- **`ytmd`**
  - 可以連結：`mygo/audio`、`innertube`、`ytmusic`、`stream`、`audio`、`auth`、`account`、`player`、`playback`
  - 不可連結：`mygo/ui`、`mygo`（App）、`locales`、`mygo/i18n`、`session`、`loader`
- **`ytm`**
  - 可以連結：`remote`、`session`、`loader`、`fetchstate`、`locales`、`noticetext`、`mygo/i18n`、`bubbletea`、`golang.org/x/term`（`ytm login` 關閉回顯用）
  - 不可連結：`mygo/ui`、`mygo`（App）、`mygo/audio`、`audio`、`auth`、`account`、`innertube`、`ytmusic`
- **`desktop`**
  - 可以連結：MyGo 全部、`remote`、`session`、`loader`、`fetchstate`、`noticetext`、`artwork`、`theme`、`settings`
  - 不可連結：`audio`、`auth`、`account`、`stream`、`innertube`、`ytmusic`

- ytmd 不連結 MyGo App 也能播放並接收媒體鍵（§12）。
- **暫時的例外：** 前端的 `session` 目前 import `account`，在同一行程內相接；P4 改成 `remote`/`api` 的型別之後，`TestDeps` 才照上面的規則檢查 Desktop 和 ytm。

## 3. 執行緒模型

- **daemon：** 由 `loop.Loop` 一個 goroutine 持有 player、playback、account、engine 和持久化狀態。現有的 `post func(func())` 改成 `loop.Post`，`playback.Wake` 也指向它。`job.Run` 不必改。
- **macOS：** 主執行緒在 `init` 以 `runtime.LockOSThread` 鎖定，`main` 執行 `CFRunLoopRun`（用 purego），讓 main dispatch queue 有人處理，供 `nowplaying_darwin.go` 的 `onMain` 使用；loop 跑在其他 goroutine。
  - **engine 只在 loop 上、主執行緒開始跑 run loop 之後才建立。** 主執行緒被鎖住卻沒有 run loop 時，`audio.New` 在其他 goroutine 會永遠等不到 main queue（實驗中卡死）；在主執行緒上呼叫則能播放，但 Now Playing 不會出現。`internal/audio` 的文件要寫明這一點。
  - 不需要 NSApplication，也不需要視窗。
  - 結束時從其他 goroutine `CFRunLoopStop`，或清理完畢後 `os.Exit`。
- **Linux、Windows：** 沒有主執行緒的限制，`main` 等待訊號即可。
- **連線：** 每條連線一個讀取 goroutine 和一個寫入 goroutine。讀取端把請求 `Post` 進 loop；loop 只把**不可變的值**交給寫入佇列，gob 編碼在寫入 goroutine 上做。**loop 絕不在 socket 上等待。**
- **前端：** `remote.Client` 的讀取 goroutine 把事件合併後，以每個前端自己的 `post` 送出：Desktop 是 `a.post`，TUI 是用 `tea.Program.Send` 送一則包著 func 的訊息，在 `Update` 中執行。同一批事件只 `post` 一次。`session` 和 `loader` 在前端照舊屬於 UI 執行緒。

## 4. 路徑與生命週期

### 4.1 路徑

`<cfg>` = `os.UserConfigDir()/yt-music-player`，`<cache>` = `os.UserCacheDir()/yt-music-player`。

| 用途 | macOS | Linux | Windows |
|---|---|---|---|
| socket | `<cfg>/ytmd.sock`，超過 103 bytes 時改用 `$TMPDIR/yt-music-player-<uid>/ytmd.sock` | `$XDG_RUNTIME_DIR/yt-music-player/ytmd.sock`，沒設時用 `/tmp/yt-music-player-<uid>/`（0700，檢查擁有者） | `%LOCALAPPDATA%\yt-music-player\ytmd.sock` |
| 單一實例鎖 | socket 旁的 `ytmd.lock`（`flock`） | 同左 | 同左（`LockFileEx`） |
| 持久化 | `<cfg>/daemon.json` | 同左 | 同左 |
| log | `<cache>/logs/ytmd.log`、`ytmd.1.log`；TUI 寫 `ytm.log` | 同左 | 同左 |
| 開機啟動 | `~/Library/LaunchAgents/dev.lanstar.ytmusicplayer.daemon.plist` | `~/.config/systemd/user/ytmd.service` | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\YTMusicPlayerDaemon` |

- socket 所在目錄為 0700，socket 本身為 0600（macOS 建立時受 umask 影響是 0755，所以要 chmod）。Windows 的 chmod 沒有作用，不做；權限靠 `%LOCALAPPDATA%` 的 ACL（§5.1）。
- **檔名後綴：** socket、鎖、持久化檔和 log 的檔名，placeholder 建置（`-tags placeholder`）加 `-placeholder`，開發建置（版本為 `dev`，§8）加 `-dev`，例如 `ytmd-dev.sock`、`daemon-placeholder-dev.json`。開發中的前端因此不會連上已安裝的正式版 daemon，也不會動到它的狀態。
- placeholder 建置的 ytmd 使用假的 Source 和 `audio.Fake`，沒有聲音。

### 4.2 按需啟動

前端呼叫 `remote.Dial`：

1. 嘗試連線，成功就交換 `Hello`（§4.3）。
2. 連線失敗就啟動（spawn）`ytmd`，不分錯誤種類；鎖（§4.1）讓多餘的啟動無害。各平台的錯誤碼只用來寫 log：Unix 是 `ENOENT`、`ECONNREFUSED`；Windows 不論檔案不存在或殘留，都是 `WSAECONNREFUSED`（要用 `x/sys/windows` 的值比對，`syscall.ECONNREFUSED` 在 Windows 上比對不到）。
   - **依序尋找 ytmd：**
     1. 環境變數 `YTMD_PATH`；
     2. `dir := filepath.Dir(filepath.EvalSymlinks(os.Executable()))`，依序找：
        - `dir/ytmd`（或 `ytmd.exe`）：Linux、Windows 的 Desktop 和 ytm；
        - `dir/ytmd.app/Contents/MacOS/ytmd`：macOS 的 ytm，它本身在 `Contents/Resources`，cask 連結的是 symlink；
        - `dir/../Resources/ytmd.app/Contents/MacOS/ytmd`：macOS 的 Desktop，它在 `Contents/MacOS`；
     3. 都找不到就回傳 `api.ErrDaemonNotFound`，訊息中提示設定 `YTMD_PATH`。開發時用 `go run` 就靠它，見 P3 的 `scripts/dev.sh`。
   - **Unix：** `exec.Command` 加 `SysProcAttr{Setsid: true}`，stdin/stdout/stderr 接 `/dev/null`。
   - **Windows：** `CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS`；ytmd 以 `-H=windowsgui` 編譯。
3. 每 50 ms 重試連線，最多等 5 s，逾時回傳 `api.ErrDaemonUnavailable`。
4. **daemon 啟動時的順序：**
   1. 取得鎖；取不到就代表已有 daemon 在跑，以 0 退出，啟動它的前端會連上原本那一個。
   2. 載入 `daemon.json`，回到上次的位置並**暫停**；只有交接後的 `resumePlaying` 例外（§4.5）。
   3. 刪除殘留的 socket 檔。
   4. `Listen`，開始接受連線。狀態在這之前就已載入，第一個連上的前端拿到的快照就是完整的。

### 4.3 存活與版本檢查（Desktop 和 TUI 都做）

兩者共用 `remote.Supervisor`；CLI 只在連線時檢查一次（§7.4）。

**狀態不走通知。** `Supervisor` 對外提供 `Health`：`Connected`、`Reconnecting`、`Unresponsive`、`Unavailable`、`DaemonOutdated`、`FrontendOutdated`。

- Desktop 依它顯示頂部橫幅，TUI 顯示在狀態列，文字來自 `locales`。
- 這是持續的狀態，不是一次性的通知，所以不經過 `tell`；`tell` 仍是所有 `notice.Notice` 唯一的出口。

**什麼時候檢查。** 為了守住「閒置時零喚醒」，**不做定時心跳**，只在以下時機檢查：

- 連線建立時：交換 `Hello`，比對 `Proto`、`Version`。
- 連線中斷時：讀到 EOF 或連線錯誤，立即判定 daemon 已停止。還沒收到回覆的請求一律以 `api.ErrDisconnected` 失敗，頁面會顯示 Retry。
- 前端回到前景時：Desktop 視窗取得焦點、TUI 收到 focus 事件，送一次 `Ping`，2 s 內要有回應。
- 請求逾時：控制類請求 2 s 沒有回應，就補送一次 `Ping`。目錄類請求可能本來就慢，不以此判斷。

`Ping` 也逾時，`Health` 就變成 `Unresponsive`，顯示「沒有回應」和「重新啟動」；之後收到任何回覆或事件就回到 `Connected`。按下「重新啟動」時：

1. 先送 `Shutdown`，3 s 後 `Hello.PID` 的行程還在，就結束它。
2. 之後當成「daemon 已停止」處理。

**daemon 已停止：用指數退避重新喚醒。**

- 依 §4.2 重連，必要時啟動 daemon。
- 等待時間從 0.25 s 開始每次加倍，上限 30 s；**只要前端還開著就一直重試**。
- 連續失敗 3 次後，`Health` 變成 `Unavailable`，提示會一直顯示到重新連上為止，並附上「重試」和「查看 log」。
- 使用者按「重試」或前端回到前景時，等待時間重設為 0.25 s 並立即重試。
- 多個前端同時喚醒時，靠鎖（§4.1）確保只會有一個 daemon。
- 重連後的第一批事件是完整快照，前端以此**取代**本地的鏡像狀態。

**daemon 版本太舊：先重啟一次，還是太舊就提醒更新。**

「太舊」的定義：`daemon.Version < 前端.Version`，依 semver 比較，任一方是 `dev` 就不比。

1. 照 §4.5 交接：daemon 存檔、退出，由這個前端啟動**自己附帶的** ytmd，接著播放。
2. 重新連上後再比對一次。如果還是太舊（例如 `YTMD_PATH` 指向舊的執行檔，或裝了兩份版本不同的程式），`Health` 就變成 `DaemonOutdated`（附 daemon 與前端的版本），請使用者更新 YT Music Player。
   - Proto 相容就繼續正常操作；不相容就停止操作，只保留提示和「查看 log」。
3. 每個前端、對每個 daemon 版本**只重啟一次**，避免兩個前端互相重啟。舊版的前端永遠不會重啟比自己新的 daemon。
4. Proto 不相容也一定能交接，因為交接所需的訊息永不改變（§5.2）。

**daemon 比前端新：**

- Proto 相容就照常使用，不提示。
- 不相容時，`Health` 變成 `FrontendOutdated`，請使用者更新 Desktop 或 `ytm`，並停止操作。

### 4.4 停止

- 關閉 Desktop（含 ⌘Q）或 TUI 只是斷開連線，daemon 繼續跑。
- 明確停止的方式：
  - `ytm daemon stop`；
  - Desktop 選單新增「結束並停止播放」；
  - 方法 `Shutdown`。
- **收到 `Shutdown`、SIGTERM 或 SIGINT 時：** 暫停 → 立即存檔 → 關閉 engine 和 controls → 關閉 listener → 刪除 socket → 釋放鎖。
- **Windows：** 沒有 SIGTERM，只能靠 `Shutdown`。沒有視窗的 `windowsgui` 程式在登出或關機時收不到通知，會直接被結束。佇列和設定最多只會晚 2 s 存檔，目前歌曲的位置則可能遺失（§6.3）。
- daemon 不因閒置而自行退出（音樂可能在暫停狀態等使用者回來）。

### 4.5 版本交接（handoff）

- 雙方在 `Hello` 交換 `Version`（semver，或 `dev`）與 `Proto`。
- **觸發者：** §4.3 的版本檢查（Desktop 和 TUI 都可以），包括更新後重新啟動的 Desktop 遇到舊 daemon 時；以及 `ytm daemon restart`。
- **流程：**
  1. 前端呼叫 `Handoff{Wait: 5s}`。
  2. daemon 只處理第一個 `Handoff`，之後的請求不回覆；那些前端和其他前端一樣，依第 3 步的事件等待。
  3. daemon 對其他連線廣播 `Event.Handoff{By: <發起者的 PID>, Wait}`。其他前端收到後，在 `Wait` 期間只重連、不啟動 daemon，以免搶先啟動另一份舊的 ytmd。
  4. daemon 存檔，並寫入 `resumePlaying: <是否正在播放>` 和 `resumeBefore: now+Wait+30s`，然後依 §4.4 退出。
  5. 發起的前端啟動自己附帶的 ytmd。
  6. 新 daemon 啟動時，若 `resumePlaying` 為真且還沒過期，就**接著播放**；否則照 §4.2 暫停。`resumePlaying` 讀取後立即清除。
  7. 發起者在 `Wait` 內沒有連上新 daemon 時（例如它自己當掉了），其他前端就照 §4.3 喚醒 daemon。
- **更新時不必特別處理，沿用 `updater/native` plugin：**
  - **Windows：** MyGo 的 `Install` 不經 NSIS，而是把安裝目錄的每一項改名成 `.<name>.old-<pid>`，再把新檔案改名進來；遇到共用衝突會重試約 4.5 s。Windows 允許改名執行中的 exe（只是不能刪除或覆寫），所以執行中的 `ytmd.exe`、`ytm.exe` 照常從改名後的檔案執行，舊檔在之後啟動 Desktop 時清掉。
  - **macOS：** `Install` 刪除舊 bundle 再換上新的；執行中的 daemon 保有原本的 inode，不受影響。**Linux** 以 rename 替換檔案，同理。
  - `Relaunch` 後的新 Desktop 遇到舊 daemon，照上面的流程交接，接著播放。開著的 TUI 照常運作，不必提示。
  - **例外：** ytmd 執行中時手動重跑 NSIS 安裝程式，`File` 無法覆寫執行中的 exe（推論會跳出 Abort/Retry/Ignore）。由 §9.3 的解除安裝與安裝步驟先停止 daemon 處理。

### 4.6 開機自動啟動（預設關閉）

由 Desktop 設定頁或 `ytm autostart on|off|status` 切換，實作在 `internal/autostart`。**切換時不影響正在跑的 daemon**，只決定下次登入時要不要啟動。

- **macOS**
  - plist 內容：`Label`、`ProgramArguments` 指向 bundle 內的 ytmd、`RunAtLoad=true`、`LimitLoadToSessionType=Aqua`、`ProcessType=Interactive`。**不設 `KeepAlive`**，否則 `stop` 之後又會被拉起來。
  - 開啟：寫入 plist，再 `launchctl enable gui/<uid>/<label>`。不做 `bootstrap`，下次登入才生效。
  - 關閉：`launchctl disable gui/<uid>/<label>`，再刪除 plist。不做 `bootout`，以免停掉正在播放的 daemon。
- **Linux**
  - unit 內容：`ExecStart=<ytmd 絕對路徑>`、`Restart=no`、`WantedBy=default.target`。
  - 開啟：寫入 unit，再 `systemctl --user enable ytmd.service`（不加 `--now`）。
  - 關閉：`systemctl --user disable ytmd.service`（不加 `--now`），再刪除檔案。
- **Windows**
  - 開啟：寫入 HKCU Run，值為 `"<ytmd.exe 路徑>"`。
  - 關閉：刪除該值。

Desktop 和 `ytm` 每次啟動時，若開機啟動是開的，就檢查並修正路徑，因為 app 被移動或更新後路徑可能改變。安裝包只附上 unit 範本；unit 和 Run 值都在使用者開啟時才寫入，所以預設是關閉的。

## 5. 協定

### 5.1 傳輸

- AF_UNIX stream，三個平台都用，不需要 named pipe。Windows 需要 10 1803 以上（Go 1.27 本身要求 Windows 10）；太舊時建立 socket 失敗（`WSAEAFNOSUPPORT`），啟動時報錯。
- **存取權限：** 只靠 socket 目錄（0700）和 socket（0600）的權限；Windows 靠 `%LOCALAPPDATA%` 的 ACL（連線需要 socket 檔的寫入權，建立需要目錄的寫入權）。
- **Windows 的 socket 檔：** 是 NTFS reparse point，當掉後會殘留，綁定時得到 `WSAEADDRINUSE`，所以照 §4.2 先刪除（`os.Remove` 可以；`os.Lstat` 回報 `ModeSocket`）。路徑以 UTF-8 傳送，上限 108 bytes，超過時一樣用 §4.1 的較短路徑。
- **每個方向一個長期存在的 gob 編解碼器：**
  - 每條連線只建一個 `gob.Encoder`，寫入 `bufio.Writer`，一批訊息寫完再 `Flush` 一次；同樣只建一個 `gob.Decoder`。
  - **不額外加長度前綴**，gob 串流本身就能分出每則訊息。
- **每次 `Decode` 前把 Frame 清成零值：** gob 不送零值，重複使用同一個 Frame 會殘留上一則的欄位。
- **列舉一律從 1 開始，0 代表無效：** `Method`、`Role`、錯誤碼、通知碼都是。gob 不送零值，收到 0 就表示欄位缺漏，一律拒絕。

### 5.2 Frame

```go
package api

const Proto = 1 // 改動既有欄位的型別或意義就加 1；增加欄位不必

type Frame struct {
	ID     uint32 // 請求從 1 起編號；事件為 0
	Hello  *Hello
	Call   *Call
	Reply  *Reply
	Cancel bool // 取消編號為 ID 的請求
	Event  *Event
}

type Hello struct {
	Proto     uint16
	Version   string
	Role      Role // Desktop、TUI、CLI
	Subscribe bool // 收事件：Desktop 和 TUI 為 true，CLI 為 false
	PID       int
}

type Call struct {
	Method Method // 列舉，從 1 開始
	// 每種參數一個指標，最多只設一個
	IDs    *IDsArgs     // Play（IDs、Start）、PlayAll、Enqueue
	Index  *IndexArgs   // Remove
	Move   *MoveArgs    // Move
	Seek   *SeekArgs
	Volume *VolumeArgs
	Query  *QueryArgs   // Search
	Ref    *RefArgs     // Album、Playlist（以 ID 指定）
	Like   *LikeArgs
	SignIn *SignInArgs  // Credentials：絕不記入 log
	Part   *PartArgs    // Refresh
	Hand   *HandoffArgs // Wait
}

type Reply struct {
	Err *Error
	// 結果，最多只設一個
	Results  *catalog.Results
	Album    *AlbumReply    // Album + Songs
	Playlist *PlaylistReply // Playlist + Songs
	Home     *catalog.Home
	Explore  *catalog.Explore
	Account  *catalog.Account
	Library  *catalog.Library
	Status   *Status // ytm status 用的完整快照
}

type Event struct {
	State   *State
	Queue   *Queue
	Songs   []catalog.Song // 佇列中新加入的歌曲資料；訂閱時的快照帶整個佇列的
	Likes   *Likes
	Account *AccountEvent // {Account, SignedIn, Switched}
	Library *catalog.Library
	Failed  *FailedEvent // {Part, Err *Error}：帳號、音樂庫或喜歡清單抓取失敗
	Notice  *Notice
	Handoff *HandoffEvent // {By int, Wait time.Duration}：相對時間，接收端用自己的時鐘換算
}
```

- 線上直接沿用 `catalog` 的純資料 struct（`Song`、`Album`……）以免多複製一次。`TestWireTypes` 會對每個型別做 gob 往返，確保裡面沒有 func、chan，也沒有需要保留的未匯出欄位。
- `interface{}` 欄位一律禁止，也不使用 `gob.Register`。
- **交接所需的訊息永不改變：**
  - 範圍：`Frame.ID`、`Hello`、`Reply.Err`、`Error`、`HandoffArgs`、`HandoffEvent`，以及 `Ping`、`Handoff`、`Shutdown` 三個方法的編號。
  - 這些欄位的名稱、型別、意義在任何 Proto 都不變。`Proto` 不相容時也能交接或關閉 daemon，就是靠這一點。
  - 由 `TestBootstrapFrozen` 比對固定的 gob 位元組來保證。

### 5.3 方法

- **播放控制：** `Play(ids, start)`（`start` 是從第幾首開始）、`PlayAll(ids)`、`Toggle`、`Pause`、`Resume`、`Next`、`Prev`、`Seek(sec)`、`SetVolume(v)`、`ToggleMute`、`CycleRepeat`、`ToggleShuffle`
- **佇列：** `Enqueue(ids, next|end)`、`Remove(index)`、`Move(from, to)`、`ClearUpNext`
- **目錄（對應 `catalog.Source`）：** `Search`、`Album`、`Playlist`、`Home`、`Explore`、`Account`、`Library`、`Like`
- **帳號（對應 `catalog.Accounts`）：** `SignIn(credentials)`、`SignOut`；`Refresh(part)` 重抓帳號、音樂庫或喜歡清單（對應 `account.Fetch`，Retry 和恢復連線時用）
- **系統：** `Ping`、`Status`、`Handoff(wait)`、`Shutdown`

其他規則：

- **取消：** 前端的 `ctx` 結束時送出 `Frame{ID, Cancel: true}`，daemon 取消對應的 `context`；斷線時取消這條連線的所有請求。

### 5.4 事件

- 訂閱的連線（`Hello.Subscribe`）會先收到每種事件的**完整快照**，之後有變化才送。
- **`State`：**
  - `{Playing, Position, Duration, Volume, Muted, Repeat, Shuffled, Current string, Index int}`，每次都送完整的值。接收端解碼前已把 Frame 清成零值（§5.1），沒送的欄位就是零值。
  - 只在播放、暫停、跳轉、換歌、緩衝開始或結束，以及音量、靜音、重複、隨機改變時送；播放中不定時送。
  - `Position` 是送出當下的位置，前端在**收到時**用自己的單調時鐘推算進度。`time.Time` 不跨行程傳送。
- **`Queue`：** `{IDs []string}`，以播放順序排列（開啟隨機時就是打亂後的順序）。佇列有變就送完整的 ID 清單（ID 很短；5000 首約 60 KB）。新加入的歌曲資料放在同一則事件的 `Songs`。
- **`Likes`：** `{Full bool, IDs []string, Songs []catalog.Song, Added []string, Removed []string}`（即 `account.Likes`）。訂閱時送 `Full`，帶喜歡的歌曲資料，前端的「喜歡的音樂」要用；之後只送差異。差異不會被背壓合併或丟棄。
- **`Account`：** `{Account, SignedIn, Switched}`（即 `account.Status`）。`Switched` 表示剛登入：之前抓的資料過時了，前端照現在的做法標成 Loading 並重抓 Home；daemon 自己重抓帳號、音樂庫和喜歡清單。
- **`Failed`：** `{Part, Err}`（即 `account.Failure`），前端的 `session` 依它把對應的 fetch 標成失敗，顯示 Retry；按下 Retry 送 `Refresh(part)`。
- **順序：** 一個請求的 `Reply` 一定先於它引起的事件送出。前端的 `session` 依賴這點：`Like` 回覆 `SignedOut` 時要先顯示「登入才能按讚」，之後的 `Account` 事件才把待處理的讚清掉。
- **`Notice`：** `{Code notice.Code, Kind, Topic, Args []string, N int}`，由前端的 `tell` 翻譯。
- **背壓：**
  - 每條連線的寫入佇列上限 64 則。
  - 佇列中還沒送出的 `State` 只保留最新一則。其他事件不合併。
  - 佇列滿了就關閉這條連線，讓前端重連並拿完整快照。

### 5.5 錯誤碼

- daemon 端用 `api.Encode(err)`，依下列順序以 `errors.Is` 比對。
- 前端用 `remote.decode` 還原成 `*remote.Error`：
  - `Unwrap()` 回傳對應的錯誤，所以 `errors.Is` 照常成立；
  - `Error()` 是對應錯誤的訊息，加上 `Detail`。Desktop 的登入表單會顯示這段文字。

錯誤碼只留前端真的會判斷的（`rg "errors\.(Is|As)\(" --glob '!*_test.go'`）：

- **`NotFound`：** `catalog.ErrNotFound`（`internal/loader/loader.go` 依它顯示 `notFound`）
- **`SignedOut`：** `catalog.ErrSignedOut`（`internal/session/session.go` 依 `Like` 的回覆顯示「登入才能按讚」）
- **`InvalidCredentials`：** `auth.ErrInvalid`（`ytm login` 的結束碼；Desktop 只顯示 `Error()` 的文字）
- **`Internal`：** `api.ErrInternal`，其他所有錯誤

沒有 `Canceled`、`Deadline`：前端取消時 `remote` 在本地回傳 `ctx.Err()`，過時的結果則由 `job` 丟掉，不靠錯誤判斷；daemon 端的逾時算 `Internal`。

補充：

- `stream.ErrUnplayable`、`auth.ErrNotStored` 只在 daemon 內部使用，不會跨行程，所以沒有錯誤碼。
- **只在前端產生、不走 socket 的錯誤：** `api.ErrDaemonNotFound`、`api.ErrDaemonUnavailable`、`api.ErrDisconnected`、`api.ErrProtoMismatch`。
- 新增前端的 `errors.Is`/`errors.As` 呼叫點時，同步加錯誤碼；`TestErrorCodes` 逐項做往返測試。
- **`Detail` 只能放安全的文字：** 不放 cookie、header、visitorData、串流網址。`Internal` 的原始錯誤只在 daemon 端以 debug 等級記錄，不傳出去。

### 5.6 通知碼

通知已是純資料：`notice.Notice{Kind, Title, Detail Code, Args []string, N int, Topic}`（`go doc ./internal/notice`），由前端以 `noticetext.Render` 翻譯（`tell`），所以 daemon 的 `playback` 不連結 `locales`，線上可以直接傳 `Notice`。

- **代碼跨版本傳送：** `Code` 依宣告順序編號，從 1 開始；新代碼只能加在最後（`NumCodes` 之前），不能重排或刪除。
- daemon 的存活與版本狀態不是通知，見 §4.3 的 `Health`。

## 6. daemon 內部

### 6.1 Service 持有的東西

`ytmusic` Source、`auth.Store`、`account`、`player`、`playback`、`audio.Engine`、媒體鍵（`controls`）、持久化。

帳號的工作已分成兩半（`go doc ./internal/account`、`go doc ./internal/session`）：`account` 不含 UI 狀態，搬進 daemon；前端的 `session` 留在 Desktop 和 ytm，把 `account` 回報的 `Change` 存成頁面要讀的狀態，並負責 `SetLiked` 的樂觀更新與還原。目前兩者在同一行程內以函式相接（`cmd/desktop/account.go`）。

- **對應到協定：** `account.Change` 的 `Status`、`Library`、`Likes`、`Failed` 就是 §5.4 的 `Account`、`Library`、`Likes`、`Failed` 事件；`account.Fetch(part)` 就是 `Refresh`（§5.3）。`Part` 目前從 0 起算，上線時照 §5.1 從 1 起編。
- **已有的行為，daemon 照舊保留：** 任何呼叫回傳 `ErrSignedOut` 就登出；每首歌以最後一次按讚為準（多個前端時，最後送出的請求為準）；`done` 先於它引起的 `Change`，即 §5.4 的「回覆先於事件」。
- **P3、P4 還要處理的：**
  - `account` 目前不保存音樂庫和喜歡清單，因為同一行程內沒人讀；daemon 要送訂閱快照（§5.4），P3 要讓它保存一份。
  - `CleanCredentials` 在 `account`，但 Desktop 用它決定「登入」按鈕能不能按；Desktop 不能連結 `account`（§2），P4 要把它移到共用的套件（例如 `api`，純函式）。
  - `auth.Store` 由 `ytmusic.Client` 持有，`account` 只透過 `catalog.Accounts` 碰到帳密；daemon 的 Service 以 store 建立 `ytmusic`，因此 store 歸 Service 所有。

### 6.2 快取

- **佇列歌曲：** daemon 只保存佇列裡歌曲的 `catalog.Song`，隨佇列增減。換帳號時清空。
- **頁面：** daemon 不快取，每次請求都向 YouTube Music 抓取；頁面資料由前端各自保存在 `catalog.Catalog`。
- 封面不經 daemon。

### 6.3 持久化（`daemon.json`，JSON v2 預設選項）

```json
{"v":1,"queue":["id",…],"index":3,"order":[…],"position":81.5,"volume":0.7,"muted":false,"repeat":0,"shuffled":false}
```

另外 handoff 時寫入 `resumePlaying` 和 `resumeBefore`（§4.5）。

- **佇列、索引、隨機順序、音量、靜音、重複、隨機有變動時：** 2 s 後存檔，期間再變就重新計時。
- **位置不在播放中定時存，以免產生喚醒。** 只在暫停、跳轉、換歌、關閉、handoff 時存。代價是 daemon 當掉或被系統直接結束時，只會回到上次暫停、跳轉或換歌時的位置。
- **寫入方式：** 先寫暫存檔再 `Rename`。讀取失敗時保留壞檔為 `daemon.json.bad`，並用空狀態啟動。

### 6.4 媒體鍵

- macOS：MediaPlayer，ytmd 是 `.app` 內 LSUIElement 的輔助程式（§9.1）。只靠主執行緒的 `CFRunLoopRun`（§3），不需要 NSApplication；直接執行 bundle 裡的程式或 `open -g` 都能出現在 Now Playing 並收到指令，Dock 不會出現圖示。
  - Control Center 顯示的名稱是 bundle 的**資料夾名稱**（`ytmd.app` 就顯示「ytmd」），`CFBundleDisplayName` 沒有作用，見 §9.1。
- Linux：MPRIS；沒有 session bus 時就不提供，並寫 debug log。前端啟動或 systemd 啟動的 ytmd 要有 `DBUS_SESSION_BUS_ADDRESS`，否則沒有媒體鍵。
- Windows：不提供。

### 6.5 Log

- daemon 的 zerolog 寫到 `logfile.Writer`：單檔 1 MiB，滿了就輪替，只保留兩個檔。
- TUI 佔用終端機，不能寫到 stderr，所以同樣寫到 `ytm.log`。Desktop 照舊。
- 等級由 `YT_LOG` 決定；要改等級就以 `YT_LOG=debug ytm daemon restart` 重新啟動 daemon。前端啟動 daemon 時會把 `YT_LOG` 傳下去；由 LaunchAgent、systemd 或 Run 啟動的則是預設的 `warn`。
- log 規則與 `AGENTS.md` 相同。
- `ytm logs` 印出 log 檔的路徑。

### 6.6 資源

- 暫停時沒有任何 timer 或 ticker，也沒有心跳。
- 沿用 `quiet.go` 的 `FreeOSMemory` 邏輯，但條件改成「3 s 內沒有處理任何請求或事件，而且 heap 增加超過 1 MB」。daemon 沒有畫面，所以不能用「3 s 沒有重建畫面」來判斷。

## 7. 前端

### 7.1 `remote.Client`

- **實作的介面：**
  - `catalog.Source` 和 `catalog.Accounts`，給前端的 `loader`、`session` 與登入流程用，頁面幾乎不用改。
  - `Controller`：播放控制與佇列的方法。
- **播放鏡像 `Mirror`：** State、Queue、Songs，只在 UI 執行緒修改。帳號、音樂庫、喜歡清單由前端的 `session` 保存（§6.1）。
- **控制：** 只送請求，`Mirror` 等 daemon 的事件才更新。回覆是錯誤，或 2 s 內沒有回覆（同 §4.3 的逾時），就 `tell` 失敗訊息。
- **拖動：** 跳轉和音量的 slider 在拖動期間用自己的本地值，不被收到的 `State` 拉回；放開後回到 `Mirror` 的值。
- **`Supervisor`：** 見 §4.3。

### 7.2 Desktop 的改動

- **`player.Player`：** Desktop 不再建立它。播放列、佇列和 Now Playing 頁讀 `Mirror`，控制呼叫 `Controller`。
- **`playback`：** 留在 daemon。原本由 `playback` 的 `SetFocused`、`SetShown`、`Follow` 排定的進度幀，改由 Desktop 的 `progress` 小元件依 `Mirror.State` 推算，並沿用現在的規則：有焦點時每秒一幀，沒焦點時拉長間隔，隱藏或最小化時不畫，暫停時不畫。
- **`session`：** 保留前端的部分（§6.1）。`account.go` 和 `signin.go` 改呼叫 `remote`。
- **選單與設定：**
  - App 選單新增「結束並停止播放」。
  - 設定新增「登入時啟動背景播放器」，預設關閉。
  - Windows 的設定另外新增「在終端機使用 `ytm`」，預設關閉（§9.3）。
  - daemon 狀態的橫幅（§4.3）。
- **測試：**
  - `testApp()` 等 helper 改成接一個在同一行程內、以 `net.Pipe` 相連的 `daemon.Service`：底下用 `audio.Fake` 和 `catalogtest`、`auth.MemoryStore`，持久化關閉，繼續遵守「不開視窗、不連網路、不寫硬碟」。
  - 現有的 Pages 和 Interactions benchmark 與預算都要保留，並新增 `interactionCases/remote-event` 和它的預算。

### 7.3 TUI（`ytm`，不帶參數時執行）

- **框架：** Bubble Tea v2（`charm.land/bubbletea/v2`，需要 Go 1.26 以上），加上 Lip Gloss v2（`charm.land/lipgloss/v2`）處理排版。頁面資料沿用前端的 `loader`、`session` 和 `fetchstate`，失敗時一樣顯示 Retry。
- **Windows 的已知問題：** 閒置時輸入迴圈每 10 ms 輪詢一次，TUI 開著時每秒約 100 次喚醒（ultraviolet #216，修正尚未合併；修正發布後就鎖定那個版本）。單獨按 Ctrl 會收到 NUL 鍵，要忽略空的按鍵訊息；Ctrl+Space 偵測不到；cmd.exe 在 alt screen 時游標仍可見；貼上的文字可能錯亂。
- **畫面：**
  - 底部固定播放列，以及 daemon 狀態（§4.3）；
  - 分頁：佇列、搜尋、音樂庫（播放清單、專輯、喜歡的歌曲）；
  - 進入專輯或播放清單可以播放、加入佇列、按讚。
- **按鍵：**
  - **播放：** `space` 播放/暫停、`n`/`p` 下一首/上一首、`←`/`→` 跳轉 5 s、`+`/`-` 音量、`s` 隨機、`r` 重複。
  - **瀏覽：** `↑`/`↓` 選擇、`enter` 播放、`a` 加入佇列、`l` 按讚、`/` 搜尋、`tab` 切換分頁、`?` 說明。
  - **離開：** `q` 或 `ctrl+c`，**不會停止 daemon**。
  - 輸入框有焦點時，按鍵只給輸入框；`esc` 離開輸入框。
- **重畫：** 只在收到訊息時重畫。進度只在「播放中、而且終端機有焦點」時每秒更新一次（用 Bubble Tea 的 focus 事件；終端機不回報 focus 時，就只看是否在播放）。
- **沒有登入時：** 顯示「請在 Desktop 登入，或在終端機執行 `ytm login`」。按 `o` 可以打開 Desktop：macOS 用 `open -b dev.lanstar.ytmusicplayer`；Linux 和 Windows 從同層目錄或 PATH 找 Desktop 執行檔。
- **語言：**
  - 以 `i18n.SetLocaleFunc` 設定，依序採用：
    1. `LC_ALL`、`LC_MESSAGES`；
    2. 系統的偏好語言：macOS 用 `CFLocaleCopyPreferredLanguages`（purego），Windows 用 `GetUserPreferredUILanguages`；
    3. `LANG`。
  - macOS 的終端機會依「地區」自動設定 `LANG`，不一定是使用者選的語言，所以排在系統偏好之後。
  - 讀到的值經 `i18n.Normalize` 後，以 `i18n.Match(preferred, i18n.Languages())` 選出語言；`Match` 回傳 `""` 時（例如 `zh_CN`、`ja`、`C`）用 `en`。
  - 不經 `App` 可用（§12）。連結 `mygo`（App）的程式不能再呼叫 `SetLocaleFunc`，因為 `mygo` 在自己的 `init` 設好了；`ytm` 不連結 App，不受影響。
- **不顯示圖片。**

### 7.4 CLI

指令：

- **`ytm`：** TUI
- **`ytm status [--json]`：** 一次性快照
- **`ytm play|pause|toggle|next|prev`：** 播放控制
- **`ytm seek <sec|+sec|-sec>`：** 跳轉
- **`ytm volume <0-100|+n|-n>`：** 音量。CLI 用 0–100，協定內部是 0–1
- **`ytm login`：** 從 stdin 讀入 cookie 並登入（見下方）
- **`ytm logout`：** 登出
- **`ytm daemon start|stop|restart|status`：** 管理 daemon；`restart` 走 §4.5 的交接
- **`ytm autostart on|off|status`：** 開機自動啟動
- **`ytm logs`：** 印出 log 檔的路徑
- **`ytm version`：** 顯示 `ytm` 與 daemon 的版本、Proto

**會不會啟動 daemon：**

- 會啟動（依 §4.2，失敗不重試）：TUI、播放控制、`seek`、`volume`、`login`、`logout`、`daemon start`、`daemon restart`。
- 不會啟動：`status`、`daemon status`、`daemon stop`、`logs`、`version`、`autostart`。
  - `daemon stop` 時 daemon 本來就沒在跑，視為成功。
  - `version` 時 daemon 沒在跑，就只顯示 `ytm` 的版本。

**版本檢查：** CLI 只在連線時檢查一次。版本太舊時只在 stderr 印出警告，不會自動重啟 daemon，以免一個腳本裡的 `ytm next` 把播放打斷。

**結束碼：** 0 成功；1 失敗；2 用法錯誤；3 daemon 沒在跑（只有 `status` 和 `daemon status` 會用到）。

**`ytm login`：**

- **讀取方式：**
  - stdin 是終端機時：先顯示怎麼複製 Cookie header 的說明（和 Desktop 登入表單同一則訊息），再用 `x/term` **關閉回顯**讀入一行。
  - stdin 是管線時：讀到 EOF 為止，例如 `pbpaste | ytm login`。
  - 輸入上限 64 KiB。
- **安全：**
  - 不提供 `--cookie` 參數，也不讀環境變數，以免 cookie 留在 shell 歷史、`ps` 或環境裡。
  - 任何錯誤訊息和 log 都不帶 cookie。
  - 讀入的資料只活到送出 `SignIn` 為止。不過 Go 的字串和 gob 編碼都會另外複製一份，沒辦法保證記憶體被清除，所以不做這項承諾。
- **處理流程：** 清理與驗證都在 daemon 端的 `account` 做（`CleanCredentials`、驗證、存入 Keychain 或檔案）。
- **結果：**
  - 成功：印出帳號名稱，結束碼 0；已連線的 Desktop 和 TUI 透過 `Account` 事件同步更新。
  - 失敗：`InvalidCredentials` 就印出翻譯過的說明，結束碼 1。

**`status --json` 格式**（`Proto` 不相容時一樣輸出，並帶 `"compatible": false`）：

```json
{"running":true,"version":"0.3.0","proto":1,"compatible":true,
 "state":"playing","position":81.5,"duration":213.0,"volume":70,"muted":false,
 "repeat":"off","shuffle":false,
 "song":{"id":"…","title":"…","artists":["…"],"album":"…"},
 "queue":{"index":3,"length":42},"account":{"signedIn":true,"name":"…"}}
```

- `state` 是 `playing`、`paused` 或 `idle`（佇列是空的）；`repeat` 是 `off`、`all` 或 `one`；`volume` 是 0–100。
- daemon 沒在跑時輸出 `{"running":false}`，結束碼 3。
- 欄位名稱是穩定的對外介面，有 golden test（`testdata/status.json`）。

## 8. 版本

- **Desktop：** 版本只寫在 `mygo.json`，以 `App.Version()` 讀取；`mygo.IsDev()` 為真時（`go run`、`mygo dev`）視為 `dev`。
- **ytmd、ytm：** 編譯時注入：

  ```sh
  -ldflags "-X yt-music-player/internal/version.Version=$(jq -r .version mygo.json)"
  ```

  沒有注入時為 `dev`，`dev` 不參與版本比較，也不觸發交接，檔名加 `-dev` 後綴（§4.1）。
- **macOS：** `ytmd.app` 的 `Info.plist` 中，`CFBundleShortVersionString` 和 `CFBundleVersion` 也由 `build-helpers.sh` 填入同一個版本。

## 9. 打包與發布

### 9.1 建置輔助執行檔

新增 `scripts/build-helpers.sh <platform>…`，在 `mygo build` 之前把 ytmd 和 ytm 編譯到根目錄的 `resources/<platform>/`（§2），讓 `mygo build` 一起簽章，並為 universal 合併架構。

- MyGo 把它們放進 `Contents/Resources/`，由內而外簽章、為 universal 合併架構，公證和更新的 tarball 都包含它們（§12）。
- universal 建置要求 `darwin-arm64` 和 `darwin-amd64` 的檔案一一對應，非執行檔必須完全相同，所以兩邊寫入同一份 `Info.plist`。
- `.gitignore` 只忽略這些平台目錄，例如 `resources/darwin-*/`；`resources/` 底下其他已提交的檔案（`icon.png` 等）照舊。
- **bundle 的資料夾名稱**就是 Control Center 顯示的名稱（§6.4），所以不叫 `ytmd.app`。建議和主程式同名 `YT Music Player.app`，附上同一個圖示：使用者在 Now Playing 看到的就是他認得的 app。要在 P8-3 確認 Spotlight 和 Launchpad 不會列出第二個同名的 app（daemon 是 `LSUIElement`，被點開也看不到任何東西）；會的話改用 `YT Music Player Helper.app`。§4.2 的路徑和 cask 跟著改；執行檔名稱仍是 `ytmd`。
- 各平台的內容：
  - **`darwin-arm64`、`darwin-amd64`：** `ytmd.app/Contents/{Info.plist,MacOS/ytmd}`（`CFBundleIdentifier=dev.lanstar.ytmusicplayer.daemon`、`CFBundleExecutable=ytmd`、`LSUIElement=true`、版本見 §8）、`ytm`
  - **`linux-amd64`、`linux-arm64`：** `ytmd`、`ytm`、`ytmd.service`（範本）
  - **`windows-amd64`、`windows-arm64`：** `ytmd.exe`（`-H=windowsgui`）、`ytm.exe`

### 9.2 macOS 與 Homebrew cask

```ruby
binary "#{appdir}/YT Music Player.app/Contents/Resources/ytm"

uninstall early_script: { executable: "#{appdir}/YT Music Player.app/Contents/Resources/ytm",
                          args: ["daemon", "stop"], must_succeed: false },
          launchctl:    "dev.lanstar.ytmusicplayer.daemon",
          delete:       ["~/Library/Application Support/yt-music-player/ytmd.sock",
                         "~/Library/Application Support/yt-music-player/ytmd.lock",
                         "~/Library/LaunchAgents/dev.lanstar.ytmusicplayer.daemon.plist"]

zap trash: ["~/Library/Application Support/yt-music-player",
            "~/Library/Caches/yt-music-player"]
```

- 用 `early_script` 是為了在移除 app 之前先停止 daemon，因為停止要用到 app 裡的 `ytm`。
- 確切的 stanza 與 `~` 的展開方式依 Homebrew 現行規格調整（P8）。改動只在 `packaging/homebrew/yt-music-player.rb`，不直接改 tap。
- socket 若因路徑過長而改放在 `$TMPDIR`（§4.1），重開機後就會消失，不必另外刪除。
- `ytmd.app` 放在 `Contents/Resources` 已通過簽章與公證（§9.1），不需要 `Contents/Helpers`。

### 9.3 Linux 與 Windows

MyGo 的三種安裝包都不能直接自訂（§12），以下是不靠 hook 的做法；同時向 MyGo 提需求：NSIS 的 include、deb 的 maintainer scripts、多個命令。

- **Linux：** `.deb`（`/opt/yt-music-player/`）和 `install.sh` 的 tar（`~/.local/yt-music-player.app/`）都會在主程式旁邊附上 `ytmd`、`ytm` 和 unit 範本（MyGo 的 resources 機制）。
  - **讓 `ytm` 在 PATH 上：** MyGo 只支援 `linux.command`，它把 `/usr/bin/<cmd>` 或 `~/.local/bin/<cmd>` 連到 **Desktop** 的執行檔，解除安裝時一併移除。所以設 `"command": "ytm"`，Desktop 啟動時第一件事是檢查自己是不是以 `ytm` 的名字被執行，是的話就 `syscall.Exec` 旁邊的 `ytm`。MyGo 在這之前只鎖定 OS 執行緒，不會啟動任何 GUI。
  - `.deb` 沒有 maintainer scripts，移除時無法停止 daemon。
  - `.deb` 依賴 `libwebkit2gtk-4.1-0` 和 GTK，對沒有桌面環境的伺服器很重。
- **Windows：** NSIS 安裝包附上 `ytmd.exe` 和 `ytm.exe`（`%LOCALAPPDATA%\Programs\YT Music Player\`）。MyGo 的 NSIS 腳本寫死在程式中，解除安裝只做 `RMDir /r`；安裝包照 MyGo 產生的用，不改寫。
  - **PATH：** Desktop 設定新增「在終端機使用 `ytm`」（只在 Windows 出現，預設關閉）。開啟時把安裝目錄加到 `HKCU\Environment` 的 `Path`（保留原本的值與 `REG_EXPAND_SZ` 型別），關閉時移除，兩者都廣播 `WM_SETTINGCHANGE`（`"Environment"`），之後開的終端機才看得到。和開機啟動一樣，Desktop 每次啟動時若它是開的，就檢查並修正路徑。
  - **解除安裝留下的東西：** HKCU Run 值（開機啟動）和 `Path` 裡的安裝目錄，都指向不存在的位置；Windows 登入時會略過 Run 值，但它仍會出現在工作管理員的「啟動」清單中。解除安裝時 `ytmd.exe` 若還在執行，`RMDir /r` 刪不掉它，安裝目錄會留到下次登出。
  - **文件說明：** 解除安裝前先在 Desktop 選「結束並停止播放」，並關閉「登入時啟動背景播放器」和「在終端機使用 `ytm`」。
  - 另外向 MyGo 提需求（NSIS 的 include 或解除安裝 hook）；有了之後改在解除安裝時執行 `ytm.exe daemon stop`、刪除 Run 值與 `Path` 項目。不採用在 CI 包一層 `makensis` 改寫腳本的做法：MyGo 改版時容易壞。

### 9.4 CI 與發布

- `release.yml` 的 macos 和 linux-windows job 在 `mygo build` 之前先執行 `scripts/build-helpers.sh`。
- 檢查步驟要確認每個平台的包裡都有 ytmd 和 ytm，並用 `file` 檢查架構。
- prek 現有的 golangci-lint（含 Linux、Windows 的 GOOS）已涵蓋整個 module，`cmd/*` 不必另外設定。`TestDeps` 透過現有的測試 hook 執行。
- **CHANGELOG：** 在 Unreleased 寫明背景播放、`ytm`、開機啟動選項，以及「macOS 升級後需重新登入一次」。

## 10. 測試、benchmark 與預算

- **`api`**
  - 測試：`TestWireTypes`、`TestBootstrapFrozen`、`TestErrorCodes`、列舉的 0 值會被拒絕、Frame 遇到未知欄位時的前向相容
  - benchmark：`BenchmarkEncode/{state,queue-5000,playlist-5000}`
- **`ipc`**
  - 測試：鎖、殘留 socket、路徑過長時的退路、`-placeholder` 與 `-dev` 後綴
  - benchmark 與預算：`BenchmarkRoundTrip/{pipe,unix}`、`BenchmarkFanout/2`；`TestBudgetStateFrame`（穩定狀態 0～2 次配置）
- **`daemon`**
  - 測試：
    - 請求分派、取消、啟動順序（狀態先載入才 Listen）；
    - 背壓：用**不讀取的 `net.Pipe`** 確認 loop 不會停住，並斷開該連線；
    - 快照加差異、背壓時 `State` 只留最新一則、持久化與延遲存檔；
    - handoff：`resumePlaying` 與它的期限、同時兩個 `Handoff` 時只處理第一個；
    - Shutdown。
  - benchmark：`BenchmarkDispatch`、`BenchmarkLikesDiff`
- **`account`**
  - 測試：P3 加上音樂庫與喜歡清單的快照
- **`remote`**
  - 測試：
    - `Supervisor` 的退避：EOF 後指數退避（用假時鐘）、上限 30 s、按「重試」或回到前景時重設；
    - `Supervisor` 的逾時與版本：`Ping` 逾時 → `Unresponsive`，收到回覆後回到 `Connected`；按「重新啟動」→ `Shutdown` → 結束行程；版本太舊 → 重啟一次 → 仍太舊時 `Health` 為 `DaemonOutdated`；同一版本只重啟一次；
    - 交接：收到 `Handoff` 事件時在 `Wait` 內不啟動 daemon；
    - 斷線時還在等待的請求回傳 `ErrDisconnected`；
    - 控制失敗或逾時時 `tell`。
- **`session`（前端）**
  - 測試：改用 `remote` 的型別之後，現有的 `Apply` 與樂觀按讚測試照樣通過
- **`autostart`**
  - 測試：對暫存目錄與假的 `launchctl`/`systemctl`/登錄檔做測試；切換時不會停止 daemon；Windows 的 `Path` 開關保留原本的值與型別，重複開啟不會加兩次，關閉只移除自己的項目
- **`tui`**
  - 測試：Bubble Tea 的 model 測試，丟入訊息後比對畫面的 golden 結果
- **`cmd/ytm`**
  - 測試：
    - `status --json` 的 golden test、結束碼、各指令會不會啟動 daemon；
    - `login`：管線輸入、64 KiB 上限、log 和錯誤訊息不帶 cookie（用測試 sink 掃描）；
    - `TestDeps`。
- **`cmd/ytmd`**
  - 測試：`TestDeps`
- **`cmd/desktop`**
  - 測試：現有測試改接在同一行程內的 Service；拖動中的 slider 不被 `State` 拉回；`TestDeps`
  - benchmark 與預算：既有的 Pages、Interactions 預算不能退步

**實機檢查**（不進 CI，加到 `AGENTS.md` 的 Commands）：

- `go test -tags audiolive -run LiveDaemon ./internal/daemon/`：會實際出聲。
- `scripts/footprint.sh -daemon`：量 ytmd 閒置、播放、關閉 Desktop 後的記憶體；同時記錄閒置喚醒次數（macOS 用 `top -stats idlew`），**暫停時必須是 0**。

## 11. 實作階段

每個階段由數個小 PR 組成，每個 PR 都要通過 `prek`。

### P2 協定與傳輸

1. `internal/api` 的型別、錯誤碼，以及交接所需的固定訊息。
2. `internal/ipc`：路徑、鎖、gob 連線、寫入佇列、背壓、spawn。
3. 對應的測試、benchmark 與預算。

### P3 daemon

1. `internal/version` 和 `internal/loop`（沒有使用者之前不先加）。
2. `internal/daemon` 的 Service：把 player、playback、account 搬入 loop；`account` 開始保存音樂庫和喜歡清單，供訂閱快照用（§6.1）。
3. 持久化。
4. `internal/logfile`。
5. `cmd/ytmd`：macOS 的主執行緒 run loop（§3）、訊號處理、Shutdown、handoff。
6. `scripts/dev.sh [-placeholder]`：把 ytmd 編譯到 `build/dev/`，再以 `YTMD_PATH` 執行 Desktop 或 ytm。

### P4 前端改走 remote

1. `internal/remote`：Source、Accounts、Controller、Mirror、`Supervisor`。
2. 前端的 `session` 改用 `remote`/`api` 的型別，不再 import `account`；`CleanCredentials` 移到共用的套件（§6.1）。之後 `TestDeps` 照 §2 檢查。
3. Desktop 改接 `remote`，並把測試 helper 改成在同一行程內接 Service。
4. 版本交接、「結束並停止播放」選單、daemon 狀態的橫幅。
5. 確認 Pages 和 Interactions 的預算沒有退步。

### P5 CLI

`ytm status [--json]`、播放控制、`login`、`logout`、`daemon`、`logs`、`version`。新增依賴 `golang.org/x/term`，在該 PR 說明理由。

### P6 TUI

1. 播放列與佇列。
2. 搜尋。
3. 音樂庫，以及專輯和播放清單頁。
4. 說明頁、未登入畫面、狀態列的 daemon 狀態、zh-Hant 翻譯。

### P7 開機自動啟動

`internal/autostart` 的三個平台實作與 Windows 的 `Path` 開關，加上 Desktop 設定頁和 `ytm autostart`。

### P8 打包與發布

1. `scripts/build-helpers.sh`，以及決定 daemon bundle 的資料夾名稱（§9.1）。
2. `release.yml`、cask、Linux 的 `linux.command` 轉接、Windows 的「在終端機使用 `ytm`」設定（§9.3）。
3. 實際走一次 draft 發布並在三個平台安裝驗證，包括 §12 列出還沒在實機上確認的項目。

### P9 文件收尾

1. 英文的 `docs/daemon.md`。
2. 更新 `AGENTS.md` 的 Layout 和 Commands、`docs/performance.md`（daemon 的記憶體數據）、README。
3. 刪除本文件。

### 相依關係

- P2 → P3 → P4。
- P5、P6 只依賴 P4-1（`remote`），可以和 P4 的其他步驟並行。
- P7 依賴 P3；它的 Desktop 設定頁依賴 P4。
- P8 依賴 P4、P5、P6。

## 12. 風險實驗的結論

實驗在 macOS arm64（MyGo v0.5.5、Go 1.27.2）上做；Linux 部分在容器內，Windows 只讀原始碼並交叉編譯。結論已寫進上面各節，這裡只留結果與尚未確認的項目。

| 實驗 | 結果 | 寫進 |
|---|---|---|
| macOS 輔助程式簽章 | `Contents/Resources` 下的 `ytmd.app` 與 `ytm` 以 Developer ID 簽章、universal、公證都通過 | §9.1、§9.2 |
| 不經 MyGo App 播放 | 可以：`internal/audio` 只用到 `mygo/audio`；macOS 已實際聽到聲音（直接執行，以及 bundle 加 run loop），Linux（ALSA、MPRIS）在容器內測過，Windows（WASAPI）依原始碼 | §2 |
| macOS 媒體鍵 | 只需鎖定主執行緒加 `CFRunLoopRun`，不需 NSApplication；直接執行或 `open -g` 都能接到 Now Playing 的指令；已實際按鍵盤的媒體鍵確認 | §3、§6.4 |
| Windows 的 AF_UNIX | 可用，不需 named pipe；連線失敗一律啟動 daemon，殘留的 socket 先刪除 | §4.1、§4.2、§5.1 |
| Windows 更新 | `Install` 以改名替換檔案，執行中的 exe 不受影響；不需安裝前的交接 | §4.5 |
| 安裝包自訂 | 三種都不能直接自訂；Linux 用 `linux.command` 轉接，Windows 由 Desktop 設定加入 PATH | §9.3 |
| TUI 的語言 | 不經 App 可用 `i18n`；Bubble Tea v2 在 Windows 有閒置喚醒等問題 | §7.3 |

**尚未在實機上確認**（P8-3）：

- 耳機與藍牙耳機的按鍵（鍵盤的媒體鍵已確認）。
- Linux 在真正的 PulseAudio／PipeWire 上播放；Windows 整條路徑，包括 `-H=windowsgui` 和沒有視窗的 WASAPI。
- Windows 10／11 上的 AF_UNIX，包括使用者名稱含中文的路徑。
- Windows 在 `ytmd.exe` 播放中、`ytm.exe` 開著時更新，以及這時重跑 NSIS 安裝程式。
- `ytmd.app` 不會出現在 LaunchServices 或 Spotlight 不該出現的地方；暫停時經 launchd 啟動的 App Nap。
- Windows Terminal 和 conhost 上的 focus、Ctrl+C、方向鍵、離開 alt screen 與游標。
