module gitlab.com/NebulousLabs/Sia

go 1.13

require (
	github.com/dchest/threefish v0.0.0-20120919164726-3ecf4c494abf
	github.com/hanwen/go-fuse/v2 v2.0.2
	github.com/inconshreveable/go-update v0.0.0-20160112193335-8152e7eb6ccf
	github.com/julienschmidt/httprouter v1.3.0
	github.com/kardianos/osext v0.0.0-20190222173326-2bc1f35cddc0
	github.com/klauspost/cpuid v1.2.2 // indirect
	github.com/klauspost/reedsolomon v1.9.3
	github.com/pkg/errors v0.9.1 // indirect
	github.com/spf13/cobra v0.0.5
	github.com/xtaci/smux v1.3.3
	gitlab.com/NebulousLabs/bolt v1.4.0
	gitlab.com/NebulousLabs/demotemutex v0.0.0-20151003192217-235395f71c40
	gitlab.com/NebulousLabs/entropy-mnemonics v0.0.0-20181018051301-7532f67e3500
	gitlab.com/NebulousLabs/errors v0.0.0-20171229012116-7ead97ef90b8
	gitlab.com/NebulousLabs/fastrand v0.0.0-20181126182046-603482d69e40
	gitlab.com/NebulousLabs/go-upnp v0.0.0-20181011194642-3a71999ed0d3
	gitlab.com/NebulousLabs/merkletree v0.0.0-20200118113624-07fbf710afc4
	gitlab.com/NebulousLabs/monitor v0.0.0-20191205095550-2b0fd3e1012a
	gitlab.com/NebulousLabs/ratelimit v0.0.0-20191111145210-66b93e150b27
	gitlab.com/NebulousLabs/siamux v0.0.0-20200210134842-f0a0c6f85ec7
	gitlab.com/NebulousLabs/threadgroup v0.0.0-20180716154133-88a11db9e46c
	gitlab.com/NebulousLabs/writeaheadlog v0.0.0-20190814160017-69f300e9bcb8
	golang.org/x/crypto v0.0.0-20200208060501-ecb85df21340
	golang.org/x/lint v0.0.0-20200302205851-738671d3881b // indirect
	golang.org/x/sys v0.0.0-20200202164722-d101bd2416d5 // indirect
	golang.org/x/tools v0.0.0-20200130002326-2f3ba24bd6e7
)

replace github.com/xtaci/smux => ./vendor/github.com/xtaci/smux
