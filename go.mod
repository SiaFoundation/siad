module gitlab.com/NebulousLabs/Sia

go 1.13

require (
	github.com/aead/chacha20 v0.0.0-20180709150244-8b13a72661da
	github.com/dchest/threefish v0.0.0-20120919164726-3ecf4c494abf
	github.com/hanwen/go-fuse/v2 v2.0.2
	github.com/inconshreveable/go-update v0.0.0-20160112193335-8152e7eb6ccf
	github.com/julienschmidt/httprouter v1.3.0
	github.com/kardianos/osext v0.0.0-20190222173326-2bc1f35cddc0
	github.com/klauspost/cpuid v1.2.2 // indirect
	github.com/klauspost/reedsolomon v1.9.3
	github.com/pkg/errors v0.9.1 // indirect
	github.com/spf13/cobra v1.0.0
	github.com/vbauerster/mpb/v5 v5.0.3
	github.com/xtaci/smux v1.3.3
	gitlab.com/NebulousLabs/bolt v1.4.4
	gitlab.com/NebulousLabs/demotemutex v0.0.0-20151003192217-235395f71c40
	gitlab.com/NebulousLabs/encoding v0.0.0-20200604091946-456c3dc907fe
	gitlab.com/NebulousLabs/entropy-mnemonics v0.0.0-20181018051301-7532f67e3500
	gitlab.com/NebulousLabs/errors v0.0.0-20200929122200-06c536cf6975
	gitlab.com/NebulousLabs/fastrand v0.0.0-20181126182046-603482d69e40
	gitlab.com/NebulousLabs/go-upnp v0.0.0-20181011194642-3a71999ed0d3
	gitlab.com/NebulousLabs/log v0.0.0-20200604091839-0ba4a941cdc2
	gitlab.com/NebulousLabs/merkletree v0.0.0-20200118113624-07fbf710afc4
	gitlab.com/NebulousLabs/monitor v0.0.0-20191205095550-2b0fd3e1012a
	gitlab.com/NebulousLabs/ratelimit v0.0.0-20200811080431-99b8f0768b2e
	gitlab.com/NebulousLabs/siamux v0.0.0-20200925101814-d7bdbba53ac2
	gitlab.com/NebulousLabs/threadgroup v0.0.0-20200608151952-38921fbef213
	gitlab.com/NebulousLabs/writeaheadlog v0.0.0-20200618142844-c59a90f49130
	golang.org/x/crypto v0.0.0-20200709230013-948cd5f35899
	golang.org/x/net v0.0.0-20200707034311-ab3426394381
	golang.org/x/sys v0.0.0-20200625212154-ddb9806d33ae // indirect
	golang.org/x/text v0.3.3 // indirect
)

replace github.com/xtaci/smux => ./vendor/github.com/xtaci/smux
