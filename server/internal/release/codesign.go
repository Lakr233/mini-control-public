package release

import (
	"crypto/x509"
	"encoding/asn1"
	"fmt"

	"github.com/blacktop/go-macho"
)

// SigningInfo holds extracted code signing metadata from a Mach-O binary.
type SigningInfo struct {
	// ID is the code directory identifier (e.g. "com.example.app")
	ID string
	// TeamID is the Apple Developer Team ID (e.g. "TEAMID")
	TeamID string
	// CommonName is the CN from the leaf signing certificate
	// (e.g. "Developer ID Application: Example Developer (TEAMID)")
	CommonName string
	// CDHash is the code directory hash
	CDHash string
}

// ExtractSigningInfo opens a Mach-O binary and extracts code signing metadata.
// Works on Linux — does not require macOS frameworks.
func ExtractSigningInfo(binaryPath string) (*SigningInfo, error) {
	// Try as single-arch Mach-O first
	f, err := macho.Open(binaryPath)
	if err != nil {
		// Try as fat/universal binary
		fat, fatErr := macho.OpenFat(binaryPath)
		if fatErr != nil {
			return nil, fmt.Errorf("not a valid Mach-O binary: %w (fat: %v)", err, fatErr)
		}
		defer fat.Close()
		if len(fat.Arches) == 0 {
			return nil, fmt.Errorf("fat binary contains no architectures")
		}
		// Use the first architecture's code signature
		return extractFromFile(fat.Arches[0].File)
	}
	defer f.Close()
	return extractFromFile(f)
}

func extractFromFile(f *macho.File) (*SigningInfo, error) {
	cs := f.CodeSignature()
	if cs == nil {
		return nil, fmt.Errorf("binary has no code signature (LC_CODE_SIGNATURE not found)")
	}

	info := &SigningInfo{}

	// Extract from CodeDirectory
	if len(cs.CodeDirectories) > 0 {
		cd := cs.CodeDirectories[0]
		info.ID = cd.ID
		info.TeamID = cd.TeamID
		info.CDHash = cd.CDHash
	}

	// Extract signing identity from CMS (PKCS#7) signature
	if len(cs.CMSSignature) > 0 {
		cn, err := extractCNFromCMS(cs.CMSSignature)
		if err != nil {
			// Fall back: construct identity from CodeDirectory fields
			if info.TeamID != "" {
				info.CommonName = fmt.Sprintf("(team: %s, id: %s)", info.TeamID, info.ID)
			}
		} else {
			info.CommonName = cn
		}
	}

	if info.TeamID == "" && info.CommonName == "" {
		return nil, fmt.Errorf("binary is ad-hoc signed (no Developer ID)")
	}

	return info, nil
}

// extractCNFromCMS parses a CMS/PKCS#7 DER blob and returns the Common Name
// from the leaf (signer) certificate.
func extractCNFromCMS(cmsData []byte) (string, error) {
	// PKCS#7 ContentInfo structure
	var contentInfo struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"explicit,tag:0"`
	}
	rest, err := asn1.Unmarshal(cmsData, &contentInfo)
	if err != nil {
		return "", fmt.Errorf("parse CMS ContentInfo: %w", err)
	}
	_ = rest

	// SignedData structure (simplified — we only need certificates)
	var signedData struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		EncapContentInfo asn1.RawValue
		Certificates     asn1.RawValue `asn1:"optional,tag:0"`
	}
	if _, err := asn1.Unmarshal(contentInfo.Content.Bytes, &signedData); err != nil {
		return "", fmt.Errorf("parse CMS SignedData: %w", err)
	}

	// Parse certificates from the SET OF Certificate
	certs, err := x509.ParseCertificates(signedData.Certificates.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse certificates from CMS: %w", err)
	}

	if len(certs) == 0 {
		return "", fmt.Errorf("no certificates found in CMS signature")
	}

	// The leaf certificate is typically the first one (the signer).
	// Look for the one that is NOT a CA (or fallback to first).
	for _, cert := range certs {
		if !cert.IsCA {
			return cert.Subject.CommonName, nil
		}
	}

	// Fallback to first cert
	return certs[0].Subject.CommonName, nil
}

// ValidateSigningIdentity checks that a binary's signing identity matches the required identity.
func ValidateSigningIdentity(binaryPath string, requiredIdentity string) error {
	info, err := ExtractSigningInfo(binaryPath)
	if err != nil {
		return fmt.Errorf("extract signing info: %w", err)
	}

	if info.CommonName != requiredIdentity {
		return fmt.Errorf("signing identity mismatch: got %q, want %q", info.CommonName, requiredIdentity)
	}

	return nil
}
