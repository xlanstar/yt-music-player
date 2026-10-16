Six seconds of tones (440 Hz left, 660 Hz right) in Opus, 48 kbit/s, two-second clusters:

	ffmpeg -f lavfi -i sine=frequency=440:duration=6:sample_rate=48000 -f lavfi -i sine=frequency=660:duration=6:sample_rate=48000 -filter_complex '[0][1]amerge=inputs=2' -c:a libopus -b:a 48k -cluster_time_limit 2000 tone.webm
	ffmpeg -i tone.webm -c copy -reserve_index_space 200 -cluster_time_limit 2000 tone_cues_first.webm

`tone.webm` has its cues after the clusters (ffmpeg's default), `tone_cues_first.webm` before them, as YouTube's files do.
