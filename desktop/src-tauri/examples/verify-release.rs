// Release-only CLI; arguments contain public artifacts, never signing secrets.
fn main() -> Result<(), String> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    if args.len() != 4 {
        return Err(
            "usage: verify-release <installer> <signature> <public-key-base64> <version>".into(),
        );
    }
    let package = std::fs::read(&args[0]).map_err(|e| e.to_string())?;
    let signature = std::fs::read_to_string(&args[1]).map_err(|e| e.to_string())?;
    let version = semver::Version::parse(&args[3]).map_err(|e| e.to_string())?;
    moviedesign_lib::updates::verify_minisign_and_version(&package, &signature, &args[2], &version)
}
