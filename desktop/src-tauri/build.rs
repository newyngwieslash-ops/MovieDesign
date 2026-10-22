fn main() {
    tauri_build::build();
    // The SDK fixture instantiates MockRuntime, which also requires Tauri's
    // existing Common Controls v6 manifest. Keep application linking unchanged.
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("windows") {
        println!(
            "cargo:rustc-link-arg-tests={}/resource.lib",
            std::env::var("OUT_DIR").unwrap()
        );
    }
}
