package solana

import (
	"encoding/binary"
	"testing"
)

func TestSystemTransferEncoding(t *testing.T) {
	from, to := MustPublicKey("11111111111111111111111111111112"), MustPublicKey("SysvarRent111111111111111111111111111111111")
	ix := SystemTransfer(from, to, 200_000_000)
	if ix.ProgramID != SystemProgram {
		t.Fatalf("program %s", ix.ProgramID)
	}
	if len(ix.Data) != 12 || binary.LittleEndian.Uint32(ix.Data[:4]) != 2 || binary.LittleEndian.Uint64(ix.Data[4:]) != 200_000_000 {
		t.Fatalf("data %x", ix.Data)
	}
	if len(ix.Accounts) != 2 || !ix.Accounts[0].IsSigner || !ix.Accounts[0].IsWritable || ix.Accounts[1].IsSigner || !ix.Accounts[1].IsWritable {
		t.Fatalf("accounts %+v", ix.Accounts)
	}
}

func TestCreateAssociatedTokenAccountIdempotentEncoding(t *testing.T) {
	owner, mint := MustPublicKey("11111111111111111111111111111112"), MustPublicKey("4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU")
	ata, err := AssociatedTokenAddress(owner, mint, TokenProgram)
	if err != nil {
		t.Fatal(err)
	}
	ix := CreateAssociatedTokenAccountIdempotent(owner, ata, owner, mint, TokenProgram)
	if ix.ProgramID != AssociatedTokenProgram || len(ix.Data) != 1 || ix.Data[0] != 1 {
		t.Fatalf("instruction %+v", ix)
	}
	if len(ix.Accounts) != 6 || ix.Accounts[1].Pubkey != ata || !ix.Accounts[0].IsSigner || ix.Accounts[5].Pubkey != TokenProgram {
		t.Fatalf("accounts %+v", ix.Accounts)
	}
}
