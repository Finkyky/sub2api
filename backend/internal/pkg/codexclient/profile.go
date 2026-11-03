package codexclient

import (
	"io"
	"math/rand/v2"
	"net/url"
	"strings"

	utls "github.com/refraction-networking/utls"
)

const Profile = "codex-linux-rustls-0.153.4"
const Version = "0.153.4"
const Originator = "codex_exec"
const UserAgent = "codex_exec/0.153.4 (Alpine Linux 3.22.1; x86_64) xterm-256color (codex_exec; 0.153.4)"

func IsCodexURL(target *url.URL) bool {
	return target != nil && strings.EqualFold(target.Hostname(), "chatgpt.com") &&
		(strings.HasPrefix(target.Path, "/backend-api/codex/") || target.Path == "/backend-api/codex")
}

func clientHello(websocket, http1Only bool) *utls.ClientHelloSpec {
	preSharedKey := &utls.UtlsPreSharedKeyExtension{}
	groups := []utls.CurveID{4588, 29, 23, 24}
	ciphers := []uint16{4866, 4865, 4867, 49196, 49195, 52393, 49200, 49199, 52392, 255}
	signatures := []utls.SignatureScheme{1283, 1027, 1539, 2055, 2054, 2053, 2052, 1537, 1281, 1025}
	extensions := []utls.TLSExtension{
		&utls.SNIExtension{},
		&utls.SupportedPointsExtension{SupportedPoints: []byte{0}},
		&utls.SupportedCurvesExtension{Curves: groups},
		&rustlsSessionTicketExtension{SessionTicketExtension: &utls.SessionTicketExtension{}, preSharedKey: preSharedKey},
	}
	if !websocket {
		protocols := []string{"h2", "http/1.1"}
		if http1Only {
			protocols = []string{"http/1.1"}
		}
		extensions = append(extensions, &utls.ALPNExtension{AlpnProtocols: protocols})
	}
	extensions = append(extensions,
		&utls.ExtendedMasterSecretExtension{},
		&utls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: signatures},
		&utls.SupportedVersionsExtension{Versions: []uint16{utls.VersionTLS13, utls.VersionTLS12}},
		&utls.PSKKeyExchangeModesExtension{Modes: []uint8{1}},
		&utls.KeyShareExtension{KeyShares: []utls.KeyShare{{Group: utls.X25519MLKEM768}, {Group: utls.X25519}}},
	)
	extensions = append(extensions, &utls.StatusRequestExtension{})
	rand.Shuffle(len(extensions), func(first, second int) {
		extensions[first], extensions[second] = extensions[second], extensions[first]
	})
	extensions = append(extensions, preSharedKey)
	return &utls.ClientHelloSpec{
		TLSVersMin: utls.VersionTLS12, TLSVersMax: utls.VersionTLS13,
		CipherSuites: ciphers, CompressionMethods: []uint8{0}, Extensions: extensions,
	}
}

type rustlsSessionTicketExtension struct {
	*utls.SessionTicketExtension
	preSharedKey *utls.UtlsPreSharedKeyExtension
}

func (extension *rustlsSessionTicketExtension) Len() int {
	if extension.preSharedKey.Len() > 0 {
		return 0
	}
	return extension.SessionTicketExtension.Len()
}

func (extension *rustlsSessionTicketExtension) Read(buffer []byte) (int, error) {
	if extension.Len() == 0 {
		return 0, io.EOF
	}
	return extension.SessionTicketExtension.Read(buffer)
}
