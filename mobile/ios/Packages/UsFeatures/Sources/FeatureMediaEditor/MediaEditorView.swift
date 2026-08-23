import SwiftUI
import UsModel
import UsDesignSystem

public enum ColorFilterPreset: String, CaseIterable, Identifiable {
    case original = "Normal"
    case noir = "B&W"
    case warm = "Sunset"
    case cool = "Nordic"
    case dramatic = "Vivid"

    public var id: String { rawValue }

    public var colorMultiply: Color {
        switch self {
        case .original: return .white
        case .noir: return .gray
        case .warm: return Color(red: 1.0, green: 0.9, blue: 0.8)
        case .cool: return Color(red: 0.85, green: 0.95, blue: 1.0)
        case .dramatic: return Color(red: 1.0, green: 0.85, blue: 0.95)
        }
    }
}

public struct MediaEditorView: View {
    public let image: UIImage?
    public let onSave: (UIImage) -> Void
    public let onDismiss: () -> Void

    @State private var selectedFilter: ColorFilterPreset = .original
    @State private var overlayText: String = ""
    @State private var isAddingText: Bool = false

    public init(
        image: UIImage? = nil,
        onSave: @escaping (UIImage) -> Void = { _ in },
        onDismiss: @escaping () -> Void = {}
    ) {
        self.image = image
        self.onSave = onSave
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                Color.black.ignoresSafeArea()

                VStack(spacing: 16) {
                    // Image Preview with filter
                    ZStack {
                        if let img = image {
                            Image(uiImage: img)
                                .resizable()
                                .scaledToFit()
                                .colorMultiply(selectedFilter.colorMultiply)
                                .saturation(selectedFilter == .noir ? 0.0 : (selectedFilter == .dramatic ? 1.4 : 1.0))
                                .frame(maxWidth: .infinity, maxHeight: .infinity)
                        } else {
                            Rectangle()
                                .fill(UsColors.bgSecondary)
                                .overlay(
                                    Image(systemName: "photo")
                                        .font(.system(size: 48))
                                        .foregroundColor(UsColors.textMuted)
                                )
                        }

                        // Text Sticker Overlay
                        if !overlayText.isEmpty {
                            Text(overlayText)
                                .font(.system(size: 24, weight: .black, design: .rounded))
                                .foregroundColor(.white)
                                .padding(.horizontal, 16)
                                .padding(.vertical, 8)
                                .background(Color.black.opacity(0.6))
                                .clipShape(RoundedRectangle(cornerRadius: 10))
                        }
                    }
                    .frame(maxHeight: 440)
                    .clipShape(RoundedRectangle(cornerRadius: 16))
                    .padding(.horizontal, 16)

                    Spacer()

                    // Filter Presets Carousel
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Filters")
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundColor(.white.opacity(0.8))
                            .padding(.horizontal, 16)

                        ScrollView(.horizontal, showsIndicators: false) {
                            HStack(spacing: 12) {
                                ForEach(ColorFilterPreset.allCases) { filter in
                                    Button(action: {
                                        selectedFilter = filter
                                        HapticManager.shared.trigger(.selection)
                                    }) {
                                        VStack(spacing: 6) {
                                            RoundedRectangle(cornerRadius: 8)
                                                .fill(filter.colorMultiply)
                                                .frame(width: 54, height: 54)
                                                .overlay(
                                                    RoundedRectangle(cornerRadius: 8)
                                                        .stroke(selectedFilter == filter ? UsColors.postbookPrimary : Color.white.opacity(0.2), lineWidth: 2)
                                                )

                                            Text(filter.rawValue)
                                                .font(.system(size: 11, weight: selectedFilter == filter ? .bold : .regular))
                                                .foregroundColor(.white)
                                        }
                                    }
                                    .buttonStyle(.plain)
                                }
                            }
                            .padding(.horizontal, 16)
                        }
                    }

                    // Action buttons
                    HStack(spacing: 16) {
                        Button(action: { isAddingText.toggle() }) {
                            HStack(spacing: 6) {
                                Image(systemName: "character.textbox")
                                Text("Add Text")
                            }
                            .font(.system(size: 14, weight: .semibold))
                            .foregroundColor(.white)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 12)
                            .background(Color.white.opacity(0.2))
                            .clipShape(Capsule())
                        }

                        Spacer()

                        Button(action: {
                            if let img = image {
                                onSave(img)
                            }
                            ToastManager.shared.show("Saved Edits", style: .success)
                            onDismiss()
                        }) {
                            Text("Done")
                                .font(.system(size: 15, weight: .bold))
                                .foregroundColor(.black)
                                .padding(.horizontal, 24)
                                .padding(.vertical, 12)
                                .background(Color.white)
                                .clipShape(Capsule())
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Edit Media")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
            .alert("Add Text Sticker", isPresented: $isAddingText) {
                TextField("Type your sticker text", text: $overlayText)
                Button("Done") {}
            }
        }
    }
}
