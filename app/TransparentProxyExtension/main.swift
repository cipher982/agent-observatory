import Foundation
import NetworkExtension

// System-extension entry point. NEProvider.startSystemExtensionMode() registers
// the provider classes declared in Info.plist (NEProviderClasses) and hands
// control to the NetworkExtension runtime; dispatchMain() parks the main thread.
autoreleasepool {
    NEProvider.startSystemExtensionMode()
}
dispatchMain()
