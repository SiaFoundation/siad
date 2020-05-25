module gitlab.com/NebulousLabs/Sia

go 1.13

require (
	github.com/aead/chacha20 v0.0.0-20180709150244-8b13a72661da
	github.com/dchest/threefish v0.0.0-20120919164726-3ecf4c494abf
	github.com/hanwen/go-fuse/v2 v2.0.3
	github.com/inconshreveable/go-update v0.0.0-20160112193335-8152e7eb6ccf
	github.com/julienschmidt/httprouter v1.3.0
	github.com/kardianos/osext v0.0.0-20190222173326-2bc1f35cddc0
	github.com/klauspost/reedsolomon v1.9.9
	github.com/mmcloughlin/avo v0.0.0-20200523190732-4439b6b2c061 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/spf13/cobra v1.0.0
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/vbauerster/mpb/v5 v5.2.2
	github.com/xtaci/smux v2.0.1+incompatible
	gitlab.com/NebulousLabs/bolt v1.4.3
	gitlab.com/NebulousLabs/demotemutex v0.0.0-20151003192217-235395f71c40
	gitlab.com/NebulousLabs/entropy-mnemonics v0.0.0-20181018051301-7532f67e3500
	gitlab.com/NebulousLabs/errors v0.0.0-20171229012116-7ead97ef90b8
	gitlab.com/NebulousLabs/fastrand v0.0.0-20181126182046-603482d69e40
	gitlab.com/NebulousLabs/go-upnp v0.0.0-20181011194642-3a71999ed0d3
	gitlab.com/NebulousLabs/merkletree v0.0.0-20200118113624-07fbf710afc4
	gitlab.com/NebulousLabs/monitor v0.0.0-20191205095550-2b0fd3e1012a
	gitlab.com/NebulousLabs/ratelimit v0.0.0-20191111145210-66b93e150b27
	gitlab.com/NebulousLabs/siamux v0.0.0-20200520080820-960bf6a42a4c
	gitlab.com/NebulousLabs/threadgroup v0.0.0-20200518123758-b458460120c6
	gitlab.com/NebulousLabs/writeaheadlog v0.0.0-20200317152015-678a773aa3e5
	golang.org/x/crypto v0.0.0-20200510223506-06a226fb4e37
	golang.org/x/net v0.0.0-20200520182314-0ba52f642ac2 // indirect
	golang.org/x/sys v0.0.0-20200523222454-059865788121 // indirect
	golang.org/x/text v0.3.2 // indirect
	golang.org/x/tools v0.0.0-20200522201501-cb1345f3a375
	gopkg.in/yaml.v2 v2.3.0 // indirect
)

replace github.com/xtaci/smux => ./vendor/github.com/xtaci/smux
