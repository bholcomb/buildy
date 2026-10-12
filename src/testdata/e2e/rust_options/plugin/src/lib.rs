#[no_mangle]
pub extern "C" fn add(a: i32, b: i32) -> i32 {
    a + b
}

// Exported only when the feature is on
#[cfg(feature = "double")]
#[no_mangle]
pub extern "C" fn double_enabled() -> i32 {
    1
}
