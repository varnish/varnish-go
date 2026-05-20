package log_test

import (
	"testing"

	varnishlog "github.com/varnish/varnish-go/log"
)

var tagNames = []string{
	"ADNS", "Backend", "BackendClose", "Backend_health", "BackendOpen",
	"BackendReuse", "BackendSSL", "BackendStart", "Begin", "BereqAcct",
	"BereqHeader", "BereqLost", "BereqMethod", "BereqProtocol", "BereqReason",
	"BereqStatus", "BereqURL", "BereqUnset", "BerespHeader", "BerespLost",
	"BerespMethod", "BerespProtocol", "BerespReason", "BerespStatus", "BerespURL",
	"BerespUnset", "Body", "BogoHeader", "Brotli", "CLI", "ConnectAcct",
	"Crypto", "DataDome", "Debug", "Edgestash", "End", "Error", "ESI_xmlerror",
	"ExpBan", "ExpKill", "Fetch_Body", "FetchError", "Filters", "Gzip",
	"H2RxBody", "H2RxHdr", "H2TxBody", "H2TxHdr", "Hash", "Hit", "HitMiss",
	"HitPass", "HttpGarbage", "Length", "Link", "LostHeader", "MSE4_ChunkFault",
	"MSE4_NewObject", "MSE4_ObjIter", "MSE4_YKEY_iter", "Nodes", "Notice",
	"OCSP", "OCSP_Error", "ObjHeader", "ObjLost", "ObjMethod", "ObjProtocol",
	"ObjReason", "ObjStatus", "ObjURL", "ObjUnset", "PipeAcct", "Proxy",
	"ProxyGarbage", "ReqAcct", "ReqHeader", "ReqLost", "ReqMethod", "ReqProtocol",
	"ReqReason", "ReqStart", "ReqStatus", "ReqTarget", "ReqURL", "ReqUnset",
	"RespHeader", "RespLost", "RespMethod", "RespProtocol", "RespReason",
	"RespStatus", "RespURL", "RespUnset", "SessClose", "SessError", "SessOpen",
	"Storage", "Timestamp", "TLS", "TTL", "VCL_acl", "VCL_call", "VCL_Error",
	"VCL_Log", "VCL_return", "VCL_trace", "VCL_use", "VdpAcct", "VfpAcct",
	"VHA6", "VSL", "WAF", "Witness", "WorkThread", "XBody", "YKEY",
}

func BenchmarkTagInit(b *testing.B) {
	for b.Loop() {
		for _, name := range tagNames {
			varnishlog.TagByName(name) //nolint:errcheck
		}
	}
}
