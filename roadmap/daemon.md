# The background player: not done

What [docs/daemon.md](../docs/daemon.md) does not yet cover. Headings follow it; once done, move the part there, written as it is.

## Known limitations

Still to check on real installs:

- headphone and Bluetooth media buttons (keyboard media keys work);
- Linux playback on real PulseAudio or PipeWire; the whole Windows path, windowless ytmd and WASAPI included;
- AF_UNIX on Windows 10 and 11, with a Chinese user name in the path;
- updating Windows while `ytmd.exe` plays and `ytm.exe` runs, and running the installer again then;
- `YT Music Player Helper.app` absent from Spotlight and Launchpad, its name and icon in Now Playing; App Nap of a paused ytmd started by launchd;
- focus, Ctrl+C, arrow keys, leaving the alt screen and the cursor in Windows Terminal and conhost.
