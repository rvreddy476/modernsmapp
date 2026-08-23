import SwiftUI
import AVKit
import UsModel
import UsDesignSystem

@Observable
public final class PIPPlayerManager: @unchecked Sendable {
    public static let shared = PIPPlayerManager()

    public var isPresented: Bool = false
    public var videoTitle: String = ""
    public var videoURL: String = ""
    public var isPlaying: Bool = true
    public var offset: CGSize = CGSize(width: 16, height: 100)

    public func present(title: String, url: String) {
        self.videoTitle = title
        self.videoURL = url
        self.isPresented = true
        self.isPlaying = true
    }

    public func dismiss() {
        self.isPresented = false
        self.isPlaying = false
    }
}

public struct FloatingPIPView: View {
    @State private var pip = PIPPlayerManager.shared
    @State private var dragOffset: CGSize = .zero

    public init() {}

    public var body: some View {
        if pip.isPresented {
            VStack(alignment: .trailing, spacing: 0) {
                ZStack(alignment: .topTrailing) {
                    // Video Content Placeholder / AVPlayer
                    ZStack(alignment: .bottomLeading) {
                        Rectangle()
                            .fill(Color(red: 0x14/255.0, green: 0x14/255.0, blue: 0x22/255.0))

                        VStack(alignment: .leading, spacing: 2) {
                            Text(pip.videoTitle)
                                .font(.system(size: 11, weight: .bold))
                                .foregroundColor(.white)
                                .lineLimit(1)
                            Text("Playing in Background")
                                .font(.system(size: 9))
                                .foregroundColor(.white.opacity(0.7))
                        }
                        .padding(8)
                    }

                    // Top control buttons (Play/Pause, Close)
                    HStack(spacing: 8) {
                        Button(action: { pip.isPlaying.toggle() }) {
                            Image(systemName: pip.isPlaying ? "pause.fill" : "play.fill")
                                .font(.system(size: 11, weight: .bold))
                                .foregroundColor(.white)
                                .padding(6)
                                .background(Color.black.opacity(0.6))
                                .clipShape(Circle())
                        }

                        Button(action: { pip.dismiss() }) {
                            Image(systemName: "xmark")
                                .font(.system(size: 11, weight: .bold))
                                .foregroundColor(.white)
                                .padding(6)
                                .background(Color.black.opacity(0.6))
                                .clipShape(Circle())
                        }
                    }
                    .padding(6)
                }
                .frame(width: 170, height: 100)
                .clipShape(RoundedRectangle(cornerRadius: 14))
                .overlay(RoundedRectangle(cornerRadius: 14).stroke(Color.white.opacity(0.2), lineWidth: 1))
                .shadow(color: Color.black.opacity(0.4), radius: 12, x: 0, y: 6)
            }
            .offset(x: pip.offset.width + dragOffset.width, y: pip.offset.height + dragOffset.height)
            .gesture(
                DragGesture()
                    .onChanged { val in
                        dragOffset = val.translation
                    }
                    .onEnded { val in
                        pip.offset.width += val.translation.width
                        pip.offset.height += val.translation.height
                        dragOffset = .zero
                    }
            )
        }
    }
}
