import BridraMobile
import Foundation

public enum BridgeHostError: Error {
    case runtimeCreationFailed
    case unexpectedCancellationMatch
}

public enum BridgeHost {
    public static func callHealth() throws -> String {
        var creationError: NSError?
        guard let runtime = BRDMobilebridgeNewRuntime(
            "embedded-simulator-token",
            &creationError
        ) else {
            throw creationError ?? BridgeHostError.runtimeCreationFailed
        }

        var callError: NSError?
        let response = runtime.callJSON(
            """
            {"id":"simulator-health","method":"system.health","meta":{"token":"embedded-simulator-token"}}
            """,
            error: &callError
        )
        if let callError {
            throw callError
        }
        if runtime.cancel("not-active") {
            throw BridgeHostError.unexpectedCancellationMatch
        }
        try runtime.close(5_000)
        return response
    }
}
