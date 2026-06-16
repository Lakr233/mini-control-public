// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "MiniControlWorker",
    platforms: [.macOS(.v14)],
    products: [
        .executable(name: "MiniControlWorker", targets: ["MiniControlWorker"]),
    ],
    dependencies: [
        .package(url: "https://github.com/apple/swift-argument-parser.git", from: "1.3.0"),
        .package(url: "https://github.com/apple/swift-log.git", from: "1.5.0"),
        .package(url: "https://github.com/jpsim/Yams.git", from: "5.0.0"),
        .package(url: "https://github.com/apple/swift-nio.git", from: "2.65.0"),
    ],
    targets: [
        .executableTarget(
            name: "MiniControlWorker",
            dependencies: [
                "WorkerCore",
                .product(name: "ArgumentParser", package: "swift-argument-parser"),
                .product(name: "Logging", package: "swift-log"),
            ],
        ),
        .target(
            name: "WorkerCore",
            dependencies: [
                .product(name: "Logging", package: "swift-log"),
                .product(name: "Yams", package: "Yams"),
                .product(name: "NIOCore", package: "swift-nio"),
                .product(name: "NIOPosix", package: "swift-nio"),
            ],
        ),
        .testTarget(
            name: "WorkerCoreTests",
            dependencies: ["WorkerCore"],
        ),
    ],
)
