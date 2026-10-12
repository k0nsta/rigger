#!/usr/bin/env python3
"""Drop the trailing APFS recovery partition from a raw tart disk image's GPT.

macOS puts the recovery container right after the system container, so after
`tart set --disk-size` the system container cannot grow into the new space, and
diskutil refuses to erase a recovery store while SIP is on. Like
packer-plugin-tart's recovery_partition = "delete", this edits the partition
table in the image file itself (primary and backup GPT); the guest then runs
`diskutil repairDisk disk0` and `diskutil apfs resizeContainer disk0s2 0`.

usage: drop-recovery.py DISK_IMG  (the VM must be stopped)
"""
import struct
import sys
import uuid
import zlib

SECTOR = 512
RECOVERY = uuid.UUID("52637672-7900-11AA-AA11-00306543ECAC")


def header(f, lba):
    f.seek(lba * SECTOR)
    raw = bytearray(f.read(SECTOR))
    if raw[:8] != b"EFI PART":
        sys.exit(f"no GPT header at LBA {lba}")
    size = struct.unpack_from("<I", raw, 12)[0]
    return raw, size


def write_header(f, lba, raw, size, entries_lba, entries_crc):
    struct.pack_into("<Q", raw, 72, entries_lba)
    struct.pack_into("<I", raw, 88, entries_crc)
    struct.pack_into("<I", raw, 16, 0)
    struct.pack_into("<I", raw, 16, zlib.crc32(bytes(raw[:size])))
    f.seek(lba * SECTOR)
    f.write(raw)


def main(path):
    with open(path, "r+b") as f:
        primary, psize = header(f, 1)
        alt_lba, entries_lba = struct.unpack_from("<Q", primary, 32)[0], struct.unpack_from("<Q", primary, 72)[0]
        count, esize = struct.unpack_from("<II", primary, 80)
        f.seek(entries_lba * SECTOR)
        entries = bytearray(f.read(count * esize))

        used = [i for i in range(count) if any(entries[i * esize:i * esize + 16])]
        hits = [i for i in used if uuid.UUID(bytes_le=bytes(entries[i * esize:i * esize + 16])) == RECOVERY]
        if not hits:
            print("no recovery partition; nothing to do")
            return
        idx = hits[0]
        if len(hits) > 1 or idx != used[-1]:
            sys.exit("the recovery partition is not the single last GPT entry; refusing")
        entries[idx * esize:(idx + 1) * esize] = bytes(esize)
        crc = zlib.crc32(bytes(entries))

        backup, bsize = header(f, alt_lba)
        backup_entries_lba = struct.unpack_from("<Q", backup, 72)[0]
        for lba in (entries_lba, backup_entries_lba):
            f.seek(lba * SECTOR)
            f.write(entries)
        write_header(f, 1, primary, psize, entries_lba, crc)
        write_header(f, alt_lba, backup, bsize, backup_entries_lba, crc)
        print(f"dropped recovery partition (GPT entry {idx + 1})")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    main(sys.argv[1])
