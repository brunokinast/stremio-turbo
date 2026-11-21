use std::{
    io::{Read, Write},
    path::PathBuf,
};

use anyhow::{anyhow, Context};
use semver::{Version, VersionReq};
use serde::Deserialize;
use sha2::{Digest, Sha256};
use url::Url;

#[derive(Debug, Clone)]
pub struct Update {
    /// The new version that we update to
    pub version: Version,
    pub file: PathBuf,
}

#[derive(Debug)]
pub struct Updater {
    pub current_version: Version,
    pub next_version: VersionReq,
    pub endpoint: Url,
    pub force_update: bool,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct UpdateResponse {
    version_desc: Url,
    version: String,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FileItem {
    // name: String,
    pub url: Url,
    pub checksum: String,
    os: String,
}
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
struct Descriptor {
    version: String,
    files: Vec<FileItem>,
}

impl Updater {
    pub fn new(current_version: Version, updater_endpoint: &Url, force_update: bool) -> Self {
        Self {
            next_version: VersionReq::parse(&format!(">{current_version}"))
                .expect("Version is type-safe"),
            current_version,
            endpoint: updater_endpoint.clone(),
            force_update,
        }
    }

    /// Fetches the latest update from the update server.
    pub fn autoupdate(&self) -> Result<Option<Update>, anyhow::Error> {
        // Check for updates
        println!("Fetching updates for v{}", self.current_version);
        println!("Using updater endpoint {}", self.endpoint);
        let update_response =
            reqwest::blocking::get(self.endpoint.clone())?.json::<UpdateResponse>()?;
        let update_descriptor = fetch_signed_descriptor(&update_response.version_desc)?;

        if update_response.version != update_descriptor.version {
            return Err(anyhow!("Mismatched update versions"));
        }
        let installer = update_descriptor
            .files
            .iter()
            .find(|file_item| file_item.os == std::env::consts::OS)
            .context("No update for this OS")?;
        let version = Version::parse(update_descriptor.version.as_str())?;
        if !self.force_update && !self.next_version.matches(&version) {
            return Err(anyhow!(
                "No new releases found that match the requirement of `{}`",
                self.next_version
            ));
        }
        println!("Found update v{version}");

        let file_name = std::path::Path::new(installer.url.path())
            .file_name()
            .context("Invalid file name")?
            .to_str()
            .context("The path is not valid UTF-8")?
            .to_string();
        let temp_dir = std::env::temp_dir();
        let dest = temp_dir.join(file_name);

        std::thread::sleep(std::time::Duration::from_secs(2));
        // Download the new setup file
        let mut installer_response = reqwest::blocking::get(installer.url.clone())?;
        let size = installer_response.content_length();
        let mut downloaded: u64 = 0;
        let mut sha256 = Sha256::new();

        println!("Downloading {} to {}", installer.url, dest.display());

        let mut chunk = [0u8; 8192];
        let mut file = std::fs::File::create(&dest)?;
        loop {
            let chunk_size = installer_response.read(&mut chunk)?;
            if chunk_size == 0 {
                break;
            }
            sha256.update(&chunk[..chunk_size]);
            file.write_all(&chunk[..chunk_size])?;
            if let Some(size) = size {
                downloaded += chunk_size as u64;
                print!("\rProgress: {}%", downloaded * 100 / size);
            } else {
                print!(".");
            }
            std::io::stdout().flush().ok();
        }
        println!();
        let actual_sha256 = format!("{:x}", sha256.finalize());
        if actual_sha256 != installer.checksum {
            std::fs::remove_file(dest)?;
            return Err(anyhow::anyhow!("Checksum verification failed"));
        }
        println!("Checksum verified.");

        let update = Some(Update {
            version,
            file: dest,
        });
        Ok(update)
    }
}

/// The public half of the key Stremio Turbo's releases are signed with.
const UPDATE_PUBLIC_KEY: &str = include_str!("../../update-key.pub");

/// Fetches the update descriptor and its detached signature
/// (`<descriptor url>.sig`, base64 ed25519 over the descriptor's bytes), and
/// parses the descriptor only when the signature matches our key. The
/// descriptor holds the installer's checksum, so this covers the installer.
fn fetch_signed_descriptor(url: &Url) -> Result<Descriptor, anyhow::Error> {
    let descriptor = reqwest::blocking::get(url.clone())?
        .error_for_status()?
        .bytes()?;
    let signature_url = Url::parse(&format!("{url}.sig"))?;
    let signature = reqwest::blocking::get(signature_url)?
        .error_for_status()?
        .text()?;
    verify_descriptor(&descriptor, &signature, UPDATE_PUBLIC_KEY)?;
    Ok(serde_json::from_slice(&descriptor)?)
}

fn verify_descriptor(
    descriptor: &[u8],
    signature: &str,
    public_key: &str,
) -> Result<(), anyhow::Error> {
    use base64::Engine;
    use ed25519_dalek::{Signature, Verifier, VerifyingKey};
    use std::convert::TryInto;
    let b64 = base64::engine::general_purpose::STANDARD;
    let key: [u8; 32] = b64
        .decode(public_key.trim())?
        .try_into()
        .map_err(|_| anyhow!("Invalid update public key"))?;
    let signature: [u8; 64] = b64
        .decode(signature.trim())?
        .try_into()
        .map_err(|_| anyhow!("Invalid update signature"))?;
    VerifyingKey::from_bytes(&key)?
        .verify(descriptor, &Signature::from_bytes(&signature))
        .map_err(|_| anyhow!("Update signature verification failed"))
}

#[cfg(test)]
mod tests {
    use super::*;
    use base64::Engine;
    use ed25519_dalek::{Signer, SigningKey};

    fn keys() -> (SigningKey, String) {
        let signing = SigningKey::from_bytes(&[7u8; 32]);
        let public =
            base64::engine::general_purpose::STANDARD.encode(signing.verifying_key().to_bytes());
        (signing, public)
    }

    fn sign(signing: &SigningKey, data: &[u8]) -> String {
        base64::engine::general_purpose::STANDARD.encode(signing.sign(data).to_bytes())
    }

    #[test]
    fn signed_descriptor_is_accepted() {
        let (signing, public) = keys();
        let descriptor = br#"{"version":"1.0.1","files":[]}"#;
        assert!(verify_descriptor(descriptor, &sign(&signing, descriptor), &public).is_ok());
    }

    #[test]
    fn tampered_descriptor_is_refused() {
        let (signing, public) = keys();
        let signature = sign(&signing, br#"{"version":"1.0.1","files":[]}"#);
        assert!(
            verify_descriptor(br#"{"version":"9.9.9","files":[]}"#, &signature, &public).is_err()
        );
    }

    #[test]
    fn other_key_is_refused() {
        let (_, public) = keys();
        let other = SigningKey::from_bytes(&[9u8; 32]);
        let descriptor = br#"{"version":"1.0.1","files":[]}"#;
        assert!(verify_descriptor(descriptor, &sign(&other, descriptor), &public).is_err());
    }

    #[test]
    fn shipped_public_key_is_valid() {
        let key = base64::engine::general_purpose::STANDARD
            .decode(UPDATE_PUBLIC_KEY.trim())
            .unwrap();
        assert_eq!(key.len(), 32);
    }
}
