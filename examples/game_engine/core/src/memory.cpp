// Memory management
int AllocateMemory(int size) {
    return size * 1024;  // Convert KB to bytes
}

int FreeMemory(int address) {
    return address > 0 ? 1 : 0;  // Success if valid address
}
