// swift-tools-version: 5.9

import PackageDescription

let package = Package(
    name: "BridraMobileSmoke",
    platforms: [.iOS(.v13)],
    products: [
        .library(name: "BridgeHost", targets: ["BridgeHost"]),
    ],
    targets: [
        .binaryTarget(
            name: "BridraMobile",
            path: "BridraMobile.xcframework"
        ),
        .target(
            name: "BridgeHost",
            dependencies: ["BridraMobile"]
        ),
        .testTarget(
            name: "BridgeHostTests",
            dependencies: ["BridgeHost"]
        ),
    ]
)
