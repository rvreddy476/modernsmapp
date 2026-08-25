import SwiftUI

public enum UsIcons {
    public static func heart(filled: Bool) -> some View {
        Image(systemName: filled ? "heart.fill" : "heart")
            .resizable()
            .scaledToFit()
    }

    public static func comment() -> some View {
        Image(systemName: "bubble.right")
            .resizable()
            .scaledToFit()
    }

    public static func repost(active: Bool = false) -> some View {
        Image(systemName: "arrow.2.squarepath")
            .resizable()
            .scaledToFit()
    }

    public static func bookmark(filled: Bool) -> some View {
        Image(systemName: "bookmark" + (filled ? ".fill" : ""))
            .resizable()
            .scaledToFit()
    }

    public static func share() -> some View {
        Image(systemName: "paperplane")
            .resizable()
            .scaledToFit()
    }

    public static func play() -> some View {
        Image(systemName: "play.fill")
            .resizable()
            .scaledToFit()
    }

    public static func more() -> some View {
        Image(systemName: "ellipsis")
            .resizable()
            .scaledToFit()
    }

    public static func home() -> some View {
        Image(systemName: "house")
            .resizable()
            .scaledToFit()
    }

    public static func explore() -> some View {
        Image(systemName: "magnifyingglass")
            .resizable()
            .scaledToFit()
    }

    public static func reels() -> some View {
        Image(systemName: "play.rectangle")
            .resizable()
            .scaledToFit()
    }

    public static func profile() -> some View {
        Image(systemName: "person")
            .resizable()
            .scaledToFit()
    }
}
