import Foundation
import SystemExtensions

enum ActivationErrorFormatter {
    static func message(for error: any Error) -> String {
        let ns = error as NSError
        guard ns.domain == OSSystemExtensionErrorDomain else {
            return error.localizedDescription
        }

        if looksLikeGatekeeperSignatureRejection(ns) {
            return "The capture extension is not accepted by macOS Gatekeeper. Install a notarized and stapled Agent Observatory release, then enable live capture again."
        }

        guard let code = OSSystemExtensionError.Code(rawValue: ns.code) else {
            return "System extension activation failed (code \(ns.code)): \(error.localizedDescription)"
        }
        switch code {
        case .extensionNotFound:
            return "Capture extension wasn't found in the running app. Quit Agent Observatory fully and relaunch it from /Applications, then try again."
        case .unsupportedParentBundleLocation:
            return "Run Agent Observatory from /Applications (not the DMG or a temp path), then enable live capture again."
        case .missingEntitlement:
            return "This build is missing the system-extension entitlement. Install the signed release build."
        case .validationFailed:
            return "The capture extension failed macOS signature validation. Install the notarized and stapled release build."
        case .forbiddenBySystemPolicy:
            return "macOS blocked the system extension. Approve Agent Observatory in System Settings -> General -> Login Items & Extensions, then try again."
        case .requestCanceled:
            return "System extension activation was canceled."
        case .requestSuperseded:
            return "A newer activation request superseded this one. Try enabling live capture again."
        case .authorizationRequired:
            return "Administrator approval is required to enable the capture extension."
        default:
            return "System extension activation failed (code \(ns.code)): \(error.localizedDescription)"
        }
    }

    private static func looksLikeGatekeeperSignatureRejection(_ error: NSError) -> Bool {
        let fields = [
            error.localizedDescription,
            error.localizedFailureReason ?? "",
            error.localizedRecoverySuggestion ?? "",
            String(describing: error.userInfo),
        ].joined(separator: "\n")
        return fields.localizedCaseInsensitiveContains("code signature")
            || fields.localizedCaseInsensitiveContains("notar")
            || fields.localizedCaseInsensitiveContains("Gatekeeper")
    }
}
