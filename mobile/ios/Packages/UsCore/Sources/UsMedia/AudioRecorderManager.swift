import SwiftUI
import AVFoundation

@Observable
public final class AudioRecorderManager: NSObject, AVAudioRecorderDelegate, @unchecked Sendable {
    public var isRecording: Bool = false
    public var recordedDuration: TimeInterval = 0
    public var soundLevels: [CGFloat] = []
    public var recordedAudioURL: URL? = nil

    private var audioRecorder: AVAudioRecorder?
    private var meterTimer: Timer?

    public override init() {
        super.init()
    }

    public func startRecording() {
        let audioSession = AVAudioSession.sharedInstance()
        do {
            try audioSession.setCategory(.playAndRecord, mode: .default, options: [.defaultToSpeaker, .allowBluetooth])
            try audioSession.setActive(true)

            let tempDir = FileManager.default.temporaryDirectory
            let fileURL = tempDir.appendingPathComponent("voicenote_\(UUID().uuidString).m4a")
            self.recordedAudioURL = fileURL

            let settings: [String: Any] = [
                AVFormatIDKey: Int(kAudioFormatMPEG4AAC),
                AVSampleRateKey: 44100.0,
                AVNumberOfChannelsKey: 1,
                AVEncoderAudioQualityKey: AVAudioQuality.high.rawValue
            ]

            audioRecorder = try AVAudioRecorder(url: fileURL, settings: settings)
            audioRecorder?.delegate = self
            audioRecorder?.isMeteringEnabled = true
            audioRecorder?.record()

            isRecording = true
            recordedDuration = 0
            soundLevels.removeAll()

            meterTimer = Timer.scheduledTimer(withTimeInterval: 0.05, repeats: true) { [weak self] _ in
                guard let self = self, let recorder = self.audioRecorder, recorder.isRecording else { return }
                recorder.updateMeters()
                let power = recorder.averagePower(forChannel: 0)
                let normalized = max(0.1, CGFloat((power + 50) / 50))
                DispatchQueue.main.async {
                    self.soundLevels.append(normalized)
                    self.recordedDuration = recorder.currentTime
                    if self.soundLevels.count > 50 {
                        self.soundLevels.removeFirst()
                    }
                }
            }
        } catch {
            isRecording = false
        }
    }

    public func stopRecording() -> URL? {
        meterTimer?.invalidate()
        meterTimer = nil
        audioRecorder?.stop()
        isRecording = false
        return recordedAudioURL
    }

    public func cancelRecording() {
        meterTimer?.invalidate()
        meterTimer = nil
        audioRecorder?.stop()
        isRecording = false
        if let url = recordedAudioURL {
            try? FileManager.default.removeItem(at: url)
        }
        recordedAudioURL = nil
        soundLevels.removeAll()
    }
}

public struct VoiceNoteRecorderView: View {
    @State private var recorder = AudioRecorderManager()
    public let onSendAudio: (URL, TimeInterval) -> Void

    public init(onSendAudio: @escaping (URL, TimeInterval) -> Void = { _, _ in }) {
        self.onSendAudio = onSendAudio
    }

    public var body: some View {
        HStack(spacing: 12) {
            if recorder.isRecording {
                // Live recording waveform indicator
                HStack(spacing: 8) {
                    Circle()
                        .fill(Color.red)
                        .frame(width: 10, height: 10)

                    Text(formatTime(recorder.recordedDuration))
                        .font(.system(size: 13, weight: .bold, design: .monospaced))
                        .foregroundColor(.white)

                    // Waveform bars
                    HStack(spacing: 3) {
                        ForEach(Array(recorder.soundLevels.suffix(20).enumerated()), id: \.offset) { _, level in
                            Capsule()
                                .fill(Color.white)
                                .frame(width: 3, height: max(4, level * 28))
                        }
                    }
                    .frame(height: 30)

                    Spacer()

                    Button("Cancel") {
                        recorder.cancelRecording()
                    }
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(Color.red)

                    Button(action: {
                        let duration = recorder.recordedDuration
                        if let url = recorder.stopRecording() {
                            onSendAudio(url, duration)
                        }
                    }) {
                        Image(systemName: "arrow.up.circle.fill")
                            .font(.system(size: 28))
                            .foregroundColor(Color.white)
                    }
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 8)
                .background(Color(red: 0x1E/255.0, green: 0x1E/255.0, blue: 0x28/255.0))
                .clipShape(Capsule())
            } else {
                Button(action: {
                    recorder.startRecording()
                }) {
                    HStack(spacing: 6) {
                        Image(systemName: "mic.fill")
                        Text("Hold to Record")
                    }
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(.white)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 10)
                    .background(Color(red: 0x2A/255.0, green: 0x2A/255.0, blue: 0x38/255.0))
                    .clipShape(Capsule())
                }
            }
        }
    }

    private func formatTime(_ time: TimeInterval) -> String {
        let mins = Int(time) / 60
        let secs = Int(time) % 60
        return String(format: "%02d:%02d", mins, secs)
    }
}
