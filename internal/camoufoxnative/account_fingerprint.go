package camoufoxnative

// AccountFingerprint returns the account fixed Camoufox fingerprint configuration, generating and saving one if it does not exist
func AccountFingerprint(options Options) (map[string]any, error) {
	return loadAccountCamoufoxConfig(options.StorageStatePath, camoufoxFirefoxMajor, options.Locale, options.Timezone)
}
