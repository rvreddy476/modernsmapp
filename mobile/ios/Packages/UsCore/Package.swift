// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "UsCore",
    platforms: [
        .iOS(.v17),
        .macOS(.v14),
    ],
    products: [
        .library(name: "UsDesignSystem", targets: ["UsDesignSystem"]),
        .library(name: "UsModel", targets: ["UsModel"]),
        .library(name: "UsNetwork", targets: ["UsNetwork"]),
        .library(name: "UsMedia", targets: ["UsMedia"]),
    ],
    dependencies: [],
    targets: [
        .target(
            name: "UsModel",
            dependencies: [],
            path: "Sources/UsModel"
        ),
        .target(
            name: "UsDesignSystem",
            dependencies: ["UsModel"],
            path: "Sources/UsDesignSystem"
        ),
        .target(
            name: "UsNetwork",
            dependencies: ["UsModel"],
            path: "Sources/UsNetwork"
        ),
        .target(
            name: "UsMedia",
            dependencies: ["UsModel", "UsDesignSystem"],
            path: "Sources/UsMedia"
        ),
        .testTarget(
            name: "UsModelTests",
            dependencies: ["UsModel"],
            path: "Tests/UsModelTests"
        ),
        .testTarget(
            name: "UsNetworkTests",
            dependencies: ["UsNetwork", "UsModel"],
            path: "Tests/UsNetworkTests"
        ),
    ]
)
