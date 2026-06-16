import CryptoKit
import Foundation

/// URLSession delegate that pins the server's TLS certificate by SHA-256 fingerprint.
/// If no fingerprint is configured, accepts any certificate (for development/self-signed).
/// Usage: `openssl x509 -in server.crt -outform DER | shasum -a 256` to get the fingerprint.
public final class CertPinningDelegate: NSObject, URLSessionDelegate, Sendable {
    private let fingerprint: String // lowercase hex, e.g. "a1b2c3..."

    public init(fingerprint: String) {
        self.fingerprint = fingerprint.lowercased().replacingOccurrences(of: ":", with: "")
        super.init()
    }

    public func urlSession(
        _: URLSession,
        didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void,
    ) {
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
              let serverTrust = challenge.protectionSpace.serverTrust
        else {
            completionHandler(.performDefaultHandling, nil)
            return
        }

        // No fingerprint configured: fall back to the platform trust store.
        if fingerprint.isEmpty {
            completionHandler(.performDefaultHandling, nil)
            return
        }

        // Extract the leaf certificate
        guard SecTrustGetCertificateCount(serverTrust) > 0,
              let certificate = SecTrustGetCertificateAtIndex(serverTrust, 0)
        else {
            completionHandler(.cancelAuthenticationChallenge, nil)
            return
        }

        // Compute SHA-256 of the DER-encoded certificate
        let certData = SecCertificateCopyData(certificate) as Data
        let hash = SHA256.hash(data: certData)
        let certFingerprint = hash.map { String(format: "%02x", $0) }.joined()

        if certFingerprint == fingerprint {
            completionHandler(.useCredential, URLCredential(trust: serverTrust))
        } else {
            completionHandler(.cancelAuthenticationChallenge, nil)
        }
    }
}
