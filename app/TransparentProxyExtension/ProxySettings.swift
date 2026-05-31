import NetworkExtension

// ProxySettings builds the NETransparentProxyNetworkSettings that tell the kernel
// which flows to hand to our provider. NENetworkRule matches on IP/port, NOT
// hostname, so we include all outbound TCP :443 and do the hostname allowlist in
// handleNewFlow via SNI. Loopback and RFC1918 are excluded so LAN/local traffic
// (and our own relay hop to the Go proxy on 127.0.0.1) never enters the provider.
enum ProxySettings {

    static func make() -> NETransparentProxyNetworkSettings {
        let settings = NETransparentProxyNetworkSettings(tunnelRemoteAddress: "127.0.0.1")

        func include(_ network: String, _ prefix: Int) -> NENetworkRule {
            NENetworkRule(
                remoteNetwork: NWHostEndpoint(hostname: network, port: "443"),
                remotePrefix: prefix,
                localNetwork: nil,
                localPrefix: 0,
                protocol: .TCP,
                direction: .outbound
            )
        }
        func exclude(_ network: String, _ prefix: Int, _ port: String) -> NENetworkRule {
            NENetworkRule(
                remoteNetwork: NWHostEndpoint(hostname: network, port: port),
                remotePrefix: prefix,
                localNetwork: nil,
                localPrefix: 0,
                protocol: .TCP,
                direction: .outbound
            )
        }

        // All outbound :443 (IPv4 + IPv6); SNI filtering happens in handleNewFlow.
        settings.includedNetworkRules = [
            include("0.0.0.0", 0),
            include("::", 0),
        ]
        // Never capture loopback or private ranges — including our own CONNECT hop
        // to the Go proxy. Excludes are evaluated before includes. Covers IPv4
        // loopback/RFC1918/link-local/CGNAT and IPv6 loopback/ULA/link-local.
        settings.excludedNetworkRules = [
            exclude("127.0.0.0", 8, "0"),
            exclude("10.0.0.0", 8, "0"),
            exclude("172.16.0.0", 12, "0"),
            exclude("192.168.0.0", 16, "0"),
            exclude("169.254.0.0", 16, "0"),   // IPv4 link-local
            exclude("100.64.0.0", 10, "0"),    // CGNAT
            exclude("::1", 128, "0"),          // IPv6 loopback
            exclude("fc00::", 7, "0"),         // IPv6 unique-local (ULA)
            exclude("fe80::", 10, "0"),        // IPv6 link-local
        ]
        return settings
    }
}
