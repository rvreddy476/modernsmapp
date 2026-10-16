package processing

import "testing"

func TestParseAudioFileProbe(t *testing.T) {
	for name, c := range map[string]struct {
		json         string
		audio, video bool
		codec        string
		ms           int
		wantErr      bool
	}{
		"m4a": {json: `{"streams":[{"codec_type":"audio","codec_name":"aac","duration":"61.500000"}],"format":{"duration":"61.520000"}}`,
			audio: true, codec: "aac", ms: 61520},
		"mp3 with cover art": {json: `{"streams":[{"codec_type":"audio","codec_name":"mp3","duration":"200.0"},{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}],"format":{"duration":"200.0"}}`,
			audio: true, codec: "mp3", ms: 200000},
		"video file": {json: `{"streams":[{"codec_type":"video","codec_name":"h264","disposition":{"attached_pic":0}},{"codec_type":"audio","codec_name":"aac","duration":"10"}],"format":{"duration":"10"}}`,
			audio: true, video: true, codec: "aac", ms: 10000},
		"ogg no stream duration": {json: `{"streams":[{"codec_type":"audio","codec_name":"vorbis"}],"format":{"duration":"301.2"}}`,
			audio: true, codec: "vorbis", ms: 301200},
		"longest wins": {json: `{"streams":[{"codec_type":"audio","codec_name":"aac","duration":"400"}],"format":{"duration":"10"}}`,
			audio: true, codec: "aac", ms: 400000},
		"nan": {json: `{"streams":[{"codec_type":"audio","codec_name":"aac","duration":"NaN"}],"format":{"duration":"N/A"}}`,
			audio: true, codec: "aac", ms: 0},
		"nothing":  {json: `{}`},
		"not json": {json: `duration=1`, wantErr: true},
	} {
		p, err := ParseAudioFileProbe([]byte(c.json))
		if (err != nil) != c.wantErr {
			t.Fatalf("%s: err %v", name, err)
		}
		if err != nil {
			continue
		}
		if p.HasAudio != c.audio || p.HasVideo != c.video || p.Codec != c.codec || p.DurationMs != c.ms {
			t.Fatalf("%s: %+v", name, p)
		}
	}
}
