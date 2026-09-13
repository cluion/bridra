// swift-tools-version: 5.9

import PackageDescription

let package = Package(
    name: "bridra_flutter",
    platforms: [.iOS("13.0")],
    products: [
        .library(name: "bridra-flutter", targets: ["bridra_flutter"]),
    ],
    targets: [
        .target(name: "bridra_flutter")
    ]
)
