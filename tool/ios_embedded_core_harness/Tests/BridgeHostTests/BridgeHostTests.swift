import Foundation
import XCTest
@testable import BridgeHost

final class BridgeHostTests: XCTestCase {
    func testEmbeddedGoRuntimeAnswersWithoutHTTP() throws {
        let response = try BridgeHost.callHealth()
        let data = try XCTUnwrap(response.data(using: .utf8))
        let decoded = try XCTUnwrap(
            JSONSerialization.jsonObject(with: data) as? [String: Any]
        )
        XCTAssertEqual(decoded["id"] as? String, "simulator-health")
        XCTAssertNil(decoded["error"])

        let result = try XCTUnwrap(decoded["result"] as? [String: Any])
        XCTAssertEqual(result["status"] as? String, "ok")
        XCTAssertEqual(result["runtime"] as? String, "Go embedded mobile")
    }
}
