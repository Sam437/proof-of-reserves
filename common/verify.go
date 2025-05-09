package common

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/Conflux-Chain/go-conflux-sdk/types/cfxaddress"
	"github.com/martinboehm/btcd/txscript"
	"github.com/martinboehm/btcutil"
	"github.com/martinboehm/btcutil/base58"
	"github.com/martinboehm/btcutil/bech32"
	"github.com/martinboehm/btcutil/chaincfg"
	"github.com/okx/go-wallet-sdk/coins/starknet"
	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/ripemd160"
	"regexp"
	"strings"

	secp_ecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	tonWallet "github.com/xssnick/tonutils-go/ton/wallet"
	"golang.org/x/crypto/sha3"
)

var (
	ErrInvalidAddr = errors.New("invalid address")
	ErrInvalidSign = errors.New("can't verify signature")
)

func VerifyBETH(addr, msg, sign string) error {
	coin := "BETH"
	msgHeader, exist := PorCoinMessageSignatureHeaderMap[coin]
	if !exist {
		return fmt.Errorf("invalid coin type %s", coin)
	}
	hash := HashEvmCoinTypeMsg(msgHeader, msg)
	var p [48]byte
	var h [32]byte
	var s [96]byte
	copy(p[:], MustDecode(addr))
	copy(s[:], MustDecode(sign))
	copy(h[:], hash[:])
	var pub Pubkey
	if err := pub.Deserialize(&p); err != nil {
		return errors.New(fmt.Sprintf("unexpected failure, failed to deserialize pubkey (%x): %v", p[:], err))
	}
	var sig Signature
	if err := sig.Deserialize(&s); err != nil {
		return errors.New(fmt.Sprintf("unexpected failure, failed to deserialize signature (%x): %v", s[:], err))
	}
	res := Verify(&pub, h[:], &sig)
	if !res {
		return errors.New("unexpected failure, failed to verify signature")
	}
	return nil
}

func VerifyTRX(addr, msg, sign string) error {
	hashFuncs := []func(string) []byte{HashTrxMsg, HashTrxMsgV2}

	for _, hashFunc := range hashFuncs {
		if verifyTRX(addr, msg, sign, hashFunc) == nil {
			return nil
		}
	}

	return ErrInvalidSign
}

func verifyTRX(addr, msg, sign string, hashFunc func(string) []byte) error {
	hash := hashFunc(msg)
	s := MustDecode(sign)
	pub, err := sigToPub(hash, s)
	if err != nil {
		return ErrInvalidSign
	}
	pubKey := pub.SerializeUncompressed()
	h := sha3.NewLegacyKeccak256()
	h.Write(pubKey[1:])
	newHash := h.Sum(nil)[12:]
	newAddr := base58.CheckEncode(newHash, GetNetWork(), base58.Sha256D)
	if addr != newAddr {
		return ErrInvalidSign
	}
	return nil
}

func UtxoCoinSigToPubKey(coin, msg, sign string) ([]byte, error) {
	msgHeader, exist := PorCoinMessageSignatureHeaderMap[coin]
	if !exist {
		return nil, fmt.Errorf("invalid coin type %s", coin)
	}
	hash := HashUtxoCoinTypeMsg(msgHeader, msg)
	b, err := base64.StdEncoding.DecodeString(sign)
	if err != nil {
		return nil, ErrInvalidSign
	}
	pub, ok, err := secp_ecdsa.RecoverCompact(b, hash)
	if err != nil || !ok || pub == nil {
		return nil, ErrInvalidSign
	}

	return pub.SerializeCompressed(), nil
}

func VerifyUtxoCoin(coin, addr, msg, sign1, sign2, script string) error {
	var pub1, pub2 []byte
	var err error
	// recover pub1 and pub2 from sign1 and sign2
	if sign1 != "" {
		pub1, err = UtxoCoinSigToPubKey(coin, msg, sign1)
		if err != nil {
			return err
		}
	}
	if sign2 != "" {
		pub2, err = UtxoCoinSigToPubKey(coin, msg, sign2)
		if err != nil {
			return err
		}
	}

	return VerifyUtxoCoinSig(coin, addr, script, pub1, pub2)
}

func VerifyUtxoCoinSig(coin, addr, script string, pub1, pub2 []byte) error {
	mainNetParams := &chaincfg.Params{}
	coinAddressType := PorCoinAddressTypeMap[coin]
	// get main net params
	switch coinAddressType {
	case "BTC":
		mainNetParams = GetBTCMainNetParams()
	case "BCH":
		mainNetParams = GetBTCMainNetParams()
		// convert cash address to legacy address
		if IsCashAddress(addr) {
			legacyAddr, err := ConvertCashAddressToLegacy(addr)
			if err != nil {
				return fmt.Errorf("convertCashAddressToLegacy failed, invalid cash address: %s, error: %v", addr, err)
			}
			addr = legacyAddr
		}
	case "LTC":
		mainNetParams = GetLTCMainNetParams()
	case "DOGE":
		mainNetParams = GetDOGEMainNetParams()
	case "DASH":
		mainNetParams = GetDASHMainNetParams()
	case "BTG":
		mainNetParams = GetBTGMainNetParams()
	case "DGB":
		mainNetParams = GetDGBMainNetParams()
	case "QTUM":
		mainNetParams = GetQTUMMainNetParams()
	case "RVN":
		mainNetParams = GetRVNMainNetParams()
	case "ZEC":
		mainNetParams = GetZECMainNetParams()
	default:
		mainNetParams = GetBTCMainNetParams()
	}
	if _, err := btcutil.DecodeAddress(addr, mainNetParams); err != nil {
		return ErrInvalidSign
	}
	addrType := GuessUtxoCoinAddressType(addr)
	switch addrType {
	case "P2PKH":
		addrPub, err := btcutil.NewAddressPubKey(pub1, mainNetParams)
		if err != nil || addrPub.EncodeAddress() != addr {
			return fmt.Errorf("address not match,coin: %s, addr: %s, recoverAddr: %s", coin, addr, addrPub.EncodeAddress())
		}
	case "P2SH":
		addrPub, err := btcutil.NewAddressScriptHash(MustDecode(script), mainNetParams)
		if err != nil {
			return fmt.Errorf("get NewAddressScriptHash failed, coin:%s, addr:%s, error:%v", coin, addr, err)
		}
		if addrPub.EncodeAddress() != addr {
			return fmt.Errorf("address not match, coin:%s, addr:%s, recoverAddr:%s", coin, addr, addrPub.EncodeAddress())
		}
		addrPub1, err := btcutil.NewAddressPubKey(pub1, mainNetParams)
		if err != nil {
			return fmt.Errorf("get pub1 NewAddressPubKey failed, coin:%s, addr:%s, error: %v", coin, addr, err)
		}
		addr1 := addrPub1.EncodeAddress()
		addrPub2, err := btcutil.NewAddressPubKey(pub2, mainNetParams)
		if err != nil {
			return fmt.Errorf("get pub2 NewAddressPubKey failed, coin:%s, addr:%s, error:%v", coin, addr, err)
		}
		addr2 := addrPub2.EncodeAddress()
		typ, pubs, _, err := txscript.ExtractPkScriptAddrs(MustDecode(script), mainNetParams)
		if typ != txscript.MultiSigTy {
			return fmt.Errorf("script type not match, coin:%s, addr:%s, srcType:%d, type:%d", coin, addr, txscript.MultiSigTy, typ)
		}
		if err != nil {
			return fmt.Errorf("script ExtractPkScriptAddrs failed, coin:%s, addr:%s, error: %v", coin, addr, err)
		}
		m := map[string]struct{}{addr1: {}, addr: {}, addr2: {}}
		for _, v := range pubs {
			delete(m, v.EncodeAddress())
		}
		if len(m) > 1 {
			return fmt.Errorf("script address not match the pubs, coin:%s, addr:%s", coin, addr)
		}
	case "P2WSH":
		pkScript := MustDecode(script)
		h := sha256.New()
		h.Write(pkScript)
		witnessProg := h.Sum(nil)
		addressWitnessScriptHash, err := btcutil.NewAddressWitnessScriptHash(witnessProg, mainNetParams)
		if err != nil {
			return fmt.Errorf("get NewAddressWitnessScriptHash failed, coin:%s, addr:%s, error:%v", coin, addr, err)
		}
		if addressWitnessScriptHash.EncodeAddress() != addr {
			return fmt.Errorf("address not match,coin: %s, addr: %s, recoverAddr: %s", coin, addr, addressWitnessScriptHash.EncodeAddress())
		}
		addrPub1, err := btcutil.NewAddressPubKey(pub1, mainNetParams)
		if err != nil {
			return fmt.Errorf("get pub1 NewAddressPubKey failed, coin:%s, addr:%s, error: %v", coin, addr, err)
		}
		addr1 := addrPub1.EncodeAddress()
		addrPub2, err := btcutil.NewAddressPubKey(pub2, mainNetParams)
		if err != nil {
			return fmt.Errorf("get pub2 NewAddressPubKey failed, coin:%s, addr:%s, error: %v", coin, addr, err)
		}
		addr2 := addrPub2.EncodeAddress()
		typ, pubs, _, err := txscript.ExtractPkScriptAddrs(MustDecode(script), mainNetParams)
		if typ != txscript.MultiSigTy {
			return fmt.Errorf("script type not match, coin:%s, addr:%s, srcType:%d, type:%d", coin, addr, txscript.MultiSigTy, typ)
		}
		if err != nil {
			return fmt.Errorf("script ExtractPkScriptAddrs failed, coin:%s, addr:%s, error: %v", coin, addr, err)
		}
		m := map[string]struct{}{addr1: {}, addr: {}, addr2: {}}
		for _, v := range pubs {
			delete(m, v.EncodeAddress())
		}
		if len(m) > 1 {
			return fmt.Errorf("script address not match the pubs, coin:%s, addr:%s", coin, addr)
		}
	}
	return nil
}

func VerifyEvmCoin(coin, addr, msg, sign string) error {
	msgHeader, exist := PorCoinMessageSignatureHeaderMap[coin]
	if !exist {
		return fmt.Errorf("invalid coin type %s, addr:%s", coin, addr)
	}
	hash := HashEvmCoinTypeMsg(msgHeader, msg)
	s := MustDecode(sign)
	pub, err := sigToPub(hash, s)
	if err != nil {
		return ErrInvalidAddr
	}

	pubToEcdsa := pub.ToECDSA()
	recoverAddr := PubkeyToAddress(*pubToEcdsa).String()

	addrType, exist := PorCoinAddressTypeMap[coin]
	if !exist {
		return fmt.Errorf("invalid coin type %s, addr:%s", coin, addr)
	}
	switch addrType {
	case "FIL":
		// convert eth address to fil address
		filAddress, err := ConvertEthAddressToFilecoinAddress(PubkeyToAddress(*pubToEcdsa).Bytes())
		if err != nil {
			return fmt.Errorf("convert eth address to fil address failed, coin:%s, addr:%s, error:%v", coin, addr, err)
		}
		recoverAddr = filAddress.String()
	case "ETH":
		if !VerifySignAddr(HexToAddress(addr), hash, s) {
			return ErrInvalidSign
		}
	}

	if strings.ToLower(addr) != strings.ToLower(recoverAddr) {
		return fmt.Errorf("recovery address not match, coin:%s, recoverAddr:%s, addr:%s", coin, recoverAddr, addr)
	}

	return nil
}

func VerifyEd25519Coin(coin, addr, msg, sign, pubkey string) error {
	msgHeader, exist := PorCoinMessageSignatureHeaderMap[coin]
	if !exist {
		return fmt.Errorf("invalid coin type %s, addr:%s", coin, addr)
	}
	hash := HashEd25519Msg(msgHeader, msg)
	res, _ := Decode(sign)
	pubkeyBytes, _ := Decode(pubkey)
	if ok := ed25519.Verify(pubkeyBytes, hash, res); !ok {
		return ErrInvalidSign
	}

	addrType, exist := PorCoinAddressTypeMap[coin]
	if !exist {
		return fmt.Errorf("invalid coin type %s, addr:%s", coin, addr)
	}
	var recoverAddrs []string
	switch addrType {
	case "SOL":
		out := [32]byte{}
		byteCount := len(pubkeyBytes)
		if byteCount == 0 {
			return ErrInvalidSign
		}
		max := 32
		if byteCount < max {
			max = byteCount
		}
		copy(out[:], pubkeyBytes[0:max])
		recoverAddrs = append(recoverAddrs, base58.Encode(out[:]))
	case "APTOS":
		publicKey := append(pubkeyBytes, 0x0)
		rAddr := "0x" + hex.EncodeToString(Sha256Hash(publicKey))
		// Short address type: if address starts with 0x0, replace.
		re, _ := regexp.Compile("^0x0*")
		recoverAddrs = append(recoverAddrs, re.ReplaceAllString(rAddr, "0x"))
		recoverAddrs = append(recoverAddrs, rAddr)
	case "SUI":
		k := make([]byte, 33)
		copy(k[1:], pubkeyBytes)
		publicKeyHash, err := blake2b.New256(nil)
		if err != nil {
			return fmt.Errorf("invalid publicKey, coin:%s, recoverAddrs:%v, addr:%s", coin, recoverAddrs, addr)
		}
		publicKeyHash.Write(k)
		h := publicKeyHash.Sum(nil)
		address := "0x" + hex.EncodeToString(h)[0:64]
		recoverAddrs = append(recoverAddrs, address)
	case "TON":
		walletV3, err := tonWallet.AddressFromPubKey(pubkeyBytes, tonWallet.V3, tonWallet.DefaultSubwallet)
		if err != nil {
			return fmt.Errorf("%s, coin: %s, addr: %s, error: %v", ErrInvalidSign, coin, addr, err)
		}
		recoverAddrs = append(recoverAddrs, walletV3.String())
		recoverAddrs = append(recoverAddrs, walletV3.Bounce(false).String())

		walletHighload, err := tonWallet.AddressFromPubKey(pubkeyBytes, tonWallet.ConfigHighloadV3{MessageTTL: 60 * 60 * 12}, 4269)
		if err != nil {
			return fmt.Errorf("%s, coin: %s, addr: %s, error: %v", ErrInvalidSign, coin, addr, err)
		}
		recoverAddrs = append(recoverAddrs, walletHighload.String())
		recoverAddrs = append(recoverAddrs, walletHighload.Bounce(false).String())
	case "DOT":
		rAddr, err := GetDotAddressFromPublicKey(pubkey)
		if err != nil {
			return fmt.Errorf("%s, coin: %s, addr: %s, error: %v", ErrInvalidSign, coin, addr, err)
		}
		recoverAddrs = append(recoverAddrs, rAddr)
	}

	for _, recoverAddr := range recoverAddrs {
		if strings.ToLower(recoverAddr) == strings.ToLower(addr) {
			return nil
		}
	}

	return fmt.Errorf("recovery address not match, coin:%s, recoverAddrs:%v, addr:%s", coin, recoverAddrs, addr)
}

func VerifyEcdsaCoin(coin, addr, msg, sign string) error {
	msgHeader, exist := PorCoinMessageSignatureHeaderMap[coin]
	if !exist {
		return fmt.Errorf("invalid coin type %s, addr:%s", coin, addr)
	}
	hash := HashEcdsaMsg(msgHeader, msg)
	s := MustDecode(sign)
	pub, err := sigToPub(hash, s)
	if err != nil {
		return ErrInvalidSign
	}
	pubKey := pub.SerializeUncompressed()

	var recoverAddr string
	addrType, exist := PorCoinAddressTypeMap[coin]
	if !exist {
		return fmt.Errorf("invalid coin type %s, addr:%s", coin, addr)
	}
	switch addrType {
	case "FIL":
		pubKeyHash := hash_cal(pubKey, payloadHashConfig)
		explen := 1 + len(pubKeyHash)
		buf := make([]byte, explen)
		var protocol byte = 1
		buf[0] = protocol
		copy(buf[1:], pubKeyHash)
		cksm := hash_cal(buf, checksumHashConfig)
		recoverAddr = "f" + fmt.Sprintf("%d", protocol) + AddressEncoding.WithPadding(-1).EncodeToString(append(pubKeyHash, cksm[:]...))
	case "CFX":
		pubToEcdsa, _ := UnmarshalPubkey(pubKey)
		ethAddr := PubkeyToAddress(*pubToEcdsa).String()
		cfxOldAddr := "0x1" + ethAddr[3:]
		cfxAddr, err := cfxaddress.New(cfxOldAddr, 1029)
		if err != nil {
			return ErrInvalidSign
		}
		recoverAddr = cfxAddr.String()
	case "ELF":
		firstBytes := sha256.Sum256(pubKey)
		secondBytes := sha256.Sum256(firstBytes[:])
		recoverAddr = encodeCheck(secondBytes[:])
	case "LUNC":
		sha := sha256.Sum256(pub.SerializeCompressed())
		hasherRIPEMD160 := ripemd160.New()
		hasherRIPEMD160.Write(sha[:])
		recoverAddr, _ = bech32.EncodeFromBase256("terra", hasherRIPEMD160.Sum(nil))
	case "ETH":
		// OKT cosmos address type (start with 'ex')
		if strings.HasPrefix(addr, "ex") {
			hash := sha3.NewLegacyKeccak256()
			hash.Write(pubKey[1:])
			addressByte := hash.Sum(nil)
			recoverAddr, _ = bech32.EncodeFromBase256("ex", addressByte[12:])
		} else {
			pubToEcdsa := pub.ToECDSA()
			recoverAddr = PubkeyToAddress(*pubToEcdsa).String()
		}
	}
	if strings.ToLower(recoverAddr) != strings.ToLower(addr) {
		return fmt.Errorf("recovery address not match, coin:%s, recoverAddr:%s, addr:%s", coin, recoverAddr, addr)
	}

	return nil
}

func VerifyStarkCoin(coin, addr, msg, sign, publicKey string) error {
	const EIP712_TEMPLATE = `{
    "accountAddress": "%s",
    "typedData": {
        "types": {
            "StarkNetDomain": [
                {
                    "name": "name",
                    "type": "felt"
                },
                {
                    "name": "version",
                    "type": "felt"
                },
                {
                    "name": "chainId",
                    "type": "felt"
                }
            ],
            "Message": [
                {
                    "name": "contents",
                    "type": "felt"
                }
            ]
        },
        "primaryType": "Message",
        "domain": {
            "name": "OKX POR MESSAGE",
            "version": "1",
            "chainId": "0x534e5f4d41494e"
        },
        "message": {
            "contents": "%s"
        }
    }
}`
	hash, err := starknet.GetMessageHashWithJson(fmt.Sprintf(EIP712_TEMPLATE, addr, msg))
	if err != nil {
		return fmt.Errorf("calculate hash error")
	}

	if VerifyMessage(hash, publicKey, sign) {
		return nil
	}

	return fmt.Errorf("recovery address not match, coin:%s, addr:%s", coin, addr)
}
