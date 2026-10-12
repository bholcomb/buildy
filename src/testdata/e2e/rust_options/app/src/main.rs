// Prints the features it was built with and its profile.
fn main() {
    let mut features = vec![];
    if cfg!(feature = "fancy") {
        features.push("fancy");
    }
    if cfg!(feature = "extra") {
        features.push("extra");
    }
    let profile = if cfg!(debug_assertions) { "debug" } else { "release" };
    println!("features={} profile={}", features.join(","), profile);
}
