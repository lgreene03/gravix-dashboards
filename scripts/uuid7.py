import uuid
import time
import sys

def uuid7():
    t_ms = int(time.time() * 1000)
    # Start with random bytes
    u = uuid.uuid4()
    b = bytearray(u.bytes)
    
    # Overwrite first 48 bits with timestamp
    b[0] = (t_ms >> 40) & 0xFF
    b[1] = (t_ms >> 32) & 0xFF
    b[2] = (t_ms >> 24) & 0xFF
    b[3] = (t_ms >> 16) & 0xFF
    b[4] = (t_ms >> 8) & 0xFF
    b[5] = t_ms & 0xFF
    
    # Set Version to 7 (0111)
    b[6] = (b[6] & 0x0F) | 0x70
    
    # Set Variant to RFC 4122 (10xx) - uuid4() already does this, but to be sure:
    b[8] = (b[8] & 0x3F) | 0x80
    
    return uuid.UUID(bytes=bytes(b))

if __name__ == "__main__":
    u = uuid7()
    # verify
    if u.version != 7:
        sys.stderr.write(f"Error: Generated version {u.version}\n")
        sys.exit(1)
    print(str(u))
