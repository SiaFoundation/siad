
// helpers
const maxUnixTSInSeconds = 9999999999;

function ParseDate(d: Date | number | string): Date {
	if (d instanceof Date) return d;
	if (typeof d === 'number') {
		if (d > maxUnixTSInSeconds) return new Date(d);
		return new Date(d * 1000); // go ts
	}
	return new Date(d);
}

function ParseNumber(v: number | string, isInt = false): number {
	if (!v) return 0;
	if (typeof v === 'number') return v;
	return (isInt ? parseInt(v) : parseFloat(v)) || 0;
}

function FromArray<T>(Ctor: { new (v: any): T }, data?: any[] | any, def = null): T[] | null {
	if (!data || !Object.keys(data).length) return def;
	const d = Array.isArray(data) ? data : [data];
	return d.map((v: any) => new Ctor(v));
}

function ToObject(o: any, typeOrCfg: any = {}, child = false): any {
	if (o == null) return null;
	if (typeof o.toObject === 'function' && child) return o.toObject();

	switch (typeof o) {
		case 'string':
			return typeOrCfg === 'number' ? ParseNumber(o) : o;
		case 'boolean':
		case 'number':
			return o;
	}

	if (o instanceof Date) {
		return typeOrCfg === 'string' ? o.toISOString() : Math.floor(o.getTime() / 1000);
	}

	if (Array.isArray(o)) return o.map((v: any) => ToObject(v, typeOrCfg, true));

	const d: any = {};

	for (const k of Object.keys(o)) {
		const v: any = o[k];
		if (v === undefined) continue;
		if (v === null) continue;
		d[k] = ToObject(v, typeOrCfg[k] || {}, true);
	}

	return d;
}

// structs
// struct2ts:go.sia.tech/core/types.Currency
export interface Currency {
	Lo: number;
	Hi: number;
}

// struct2ts:go.sia.tech/siad/v2/api/renterd.WalletBalanceResponse
export interface WalletBalanceResponse {
	siacoins: Currency;
}

// struct2ts:go.sia.tech/core/types.ElementID
export interface ElementID {
	Source: number[];
	Index: number;
}

// struct2ts:go.sia.tech/core/types.SiacoinElement
export interface SiacoinElement {
	ID: ElementID;
	LeafIndex: number;
	MerkleProof: Hash256[] | null;
	Value: Currency;
	Address: number[];
	MaturityHeight: number;
}

// struct2ts:go.sia.tech/core/types.SiacoinInput
export interface SiacoinInput {
	Parent: SiacoinElement;
	SpendPolicy: any;
	Signatures: Signature[] | null;
}

// struct2ts:go.sia.tech/core/types.SiacoinOutput
export interface SiacoinOutput {
	Value: Currency;
	Address: number[];
}

// struct2ts:go.sia.tech/core/types.SiafundElement
export interface SiafundElement {
	ID: ElementID;
	LeafIndex: number;
	MerkleProof: Hash256[] | null;
	Value: number;
	Address: number[];
	ClaimStart: Currency;
}

// struct2ts:go.sia.tech/core/types.SiafundInput
export interface SiafundInput {
	Parent: SiafundElement;
	ClaimAddress: number[];
	SpendPolicy: any;
	Signatures: Signature[] | null;
}

// struct2ts:go.sia.tech/core/types.SiafundOutput
export interface SiafundOutput {
	Value: number;
	Address: number[];
}

// struct2ts:go.sia.tech/core/types.FileContract
export interface FileContract {
	Filesize: number;
	FileMerkleRoot: number[];
	WindowStart: number;
	WindowEnd: number;
	RenterOutput: SiacoinOutput;
	HostOutput: SiacoinOutput;
	MissedHostValue: Currency;
	TotalCollateral: Currency;
	RenterPublicKey: number[];
	HostPublicKey: number[];
	RevisionNumber: number;
	RenterSignature: number[];
	HostSignature: number[];
}

// struct2ts:go.sia.tech/core/types.FileContractElement
export interface FileContractElement {
	ID: ElementID;
	LeafIndex: number;
	MerkleProof: Hash256[] | null;
	Filesize: number;
	FileMerkleRoot: number[];
	WindowStart: number;
	WindowEnd: number;
	RenterOutput: SiacoinOutput;
	HostOutput: SiacoinOutput;
	MissedHostValue: Currency;
	TotalCollateral: Currency;
	RenterPublicKey: number[];
	HostPublicKey: number[];
	RevisionNumber: number;
	RenterSignature: number[];
	HostSignature: number[];
}

// struct2ts:go.sia.tech/core/types.FileContractRevision
export interface FileContractRevision {
	Parent: FileContractElement;
	Revision: FileContract;
}

// struct2ts:go.sia.tech/core/types.FileContractRenewal
export interface FileContractRenewal {
	FinalRevision: FileContract;
	InitialRevision: FileContract;
	RenterRollover: Currency;
	HostRollover: Currency;
	RenterSignature: number[];
	HostSignature: number[];
}

// struct2ts:go.sia.tech/core/types.ChainIndex
export interface ChainIndex {
	Height: number;
	ID: number[];
}

// struct2ts:go.sia.tech/core/types.StorageProof
export interface StorageProof {
	WindowStart: ChainIndex;
	WindowProof: Hash256[] | null;
	Leaf: number[];
	Proof: Hash256[] | null;
}

// struct2ts:go.sia.tech/core/types.FileContractResolution
export interface FileContractResolution {
	Parent: FileContractElement;
	Renewal: FileContractRenewal;
	StorageProof: StorageProof;
	Finalization: FileContract;
}

// struct2ts:go.sia.tech/core/types.Attestation
export interface Attestation {
	PublicKey: number[];
	Key: string;
	Value: number[] | null;
	Signature: number[];
}

// struct2ts:go.sia.tech/core/types.Transaction
export interface Transaction {
	SiacoinInputs: SiacoinInput[] | null;
	SiacoinOutputs: SiacoinOutput[] | null;
	SiafundInputs: SiafundInput[] | null;
	SiafundOutputs: SiafundOutput[] | null;
	FileContracts: FileContract[] | null;
	FileContractRevisions: FileContractRevision[] | null;
	FileContractResolutions: FileContractResolution[] | null;
	Attestations: Attestation[] | null;
	ArbitraryData: number[] | null;
	NewFoundationAddress: number[];
	MinerFee: Currency;
}

// struct2ts:go.sia.tech/siad/v2/wallet.Transaction
export interface Transaction {
	Raw: Transaction;
	Index: ChainIndex;
	ID: number[];
	Inflow: Currency;
	Outflow: Currency;
	Timestamp: Date;
}

// struct2ts:go.sia.tech/siad/v2/api/renterd.SyncerPeerResponse
export interface SyncerPeerResponse {
	netAddress: string;
}

// struct2ts:go.sia.tech/siad/v2/api/renterd.SyncerConnectRequest
export interface SyncerConnectRequest {
	netAddress: string;
}

// struct2ts:go.sia.tech/siad/v2/api/renterd.RHPScanRequest
export interface RHPScanRequest {
	netAddress: string;
	hostKey: number[];
}

// struct2ts:go.sia.tech/core/net/rhp.HostSettings
export interface HostSettings {
	acceptingContracts: boolean;
	address: number[];
	blockHeight: number;
	ephemeralAccountExpiry: number;
	maxCollateral: Currency;
	maxDuration: number;
	maxEphemeralAccountBalance: Currency;
	netAddress: string;
	remainingRegistryEntries: number;
	remainingStorage: number;
	sectorSize: number;
	totalRegistryEntries: number;
	totalStorage: number;
	validUntil: Date;
	version: string;
	windowSize: number;
	contractFee: Currency;
	collateral: Currency;
	downloadBandwidthPrice: Currency;
	uploadBandwidthPrice: Currency;
	storagePrice: Currency;
	rpcAccountBalanceCost: Currency;
	rpcFundAccountCost: Currency;
	rpcHostSettingsCost: Currency;
	rpcLatestRevisionCost: Currency;
	rpcRenewContractCost: Currency;
	progInitBaseCost: Currency;
	progMemorytimecost: Currency;
	progReadCost: Currency;
	progWriteCost: Currency;
	instrAppendSectorsBaseCost: Currency;
	instrDropSectorsBaseCost: Currency;
	instrDropSectorsUnitCost: Currency;
	instrHasSectorBaseCost: Currency;
	instrReadBaseCost: Currency;
	instrReadRegistryBaseCost: Currency;
	instrRevisionBaseCost: Currency;
	instrSectorRootsBaseCost: Currency;
	instrSwapSectorCost: Currency;
	instrUpdateRegistryBaseCost: Currency;
	instrUpdateSectorBaseCost: Currency;
	instrWriteBaseCost: Currency;
}

// struct2ts:go.sia.tech/siad/v2/api/renterd.RHPFormRequest
export interface RHPFormRequest {
	renterKey: number[] | null;
	netAddress: string;
	hostKey: number[];
	hostFunds: Currency;
	renterFunds: Currency;
	endHeight: number;
	hostSettings: HostSettings;
}

// struct2ts:go.sia.tech/core/net/rhp.Contract
export interface Contract {
	ID: ElementID;
	Revision: FileContract;
}

// struct2ts:go.sia.tech/siad/v2/api/renterd.RHPFormResponse
export interface RHPFormResponse {
	contract: Contract;
	parent: Transaction;
}

// struct2ts:go.sia.tech/siad/v2/api/renterd.RHPRenewRequest
export interface RHPRenewRequest {
	renterKey: number[] | null;
	netAddress: string;
	hostKey: number[];
	renterFunds: Currency;
	hostCollateral: Currency;
	extension: number;
	contract: FileContractRevision;
	hostSettings: HostSettings;
}

// struct2ts:go.sia.tech/siad/v2/api/renterd.RHPRenewResponse
export interface RHPRenewResponse {
	contract: Contract;
	parent: Transaction;
}

// struct2ts:go.sia.tech/core/net/rhp.RPCReadRequestSection
export interface RPCReadRequestSection {
	MerkleRoot: number[];
	Offset: number;
	Length: number;
}

// struct2ts:go.sia.tech/siad/v2/api/renterd.RHPReadRequest
export interface RHPReadRequest {
	hostKey: number[];
	netAddress: string;
	contractID: ElementID;
	renterKey: number[] | null;
	maxPrice: Currency;
	sections: RPCReadRequestSection[] | null;
}

// struct2ts:go.sia.tech/siad/v2/api/renterd.RHPAppendRequest
export interface RHPAppendRequest {
	hostKey: number[];
	netAddress: string;
	contractID: ElementID;
	renterKey: number[] | null;
	maxPrice: Currency;
}

// struct2ts:go.sia.tech/siad/v2/api/renterd.RHPAppendResponse
export interface RHPAppendResponse {
	sectorRoot: number[];
}

// exports
export {
	ParseDate,
	ParseNumber,
	FromArray,
	ToObject,
};

type Hash256 = string;
type Signature = string;
