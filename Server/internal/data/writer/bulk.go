package writer

// executeBulkWithBisect runs a batch once and recursively isolates data errors.
// Infrastructure errors stop immediately. Successful sub-batches are always
// committed from left to right, so the persisted source cursor remains a
// contiguous prefix and can be used safely for keyset resume.
func executeBulkWithBisect[T any](
	items []T,
	execute func([]T) error,
	reject func(T, error) error,
) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	err := execute(items)
	if err == nil {
		return 0, nil
	}
	if !isSplittableBulkError(err) {
		return 0, err
	}
	if len(items) == 1 {
		if rejectErr := reject(items[0], err); rejectErr != nil {
			return 0, rejectErr
		}
		return 1, nil
	}
	middle := len(items) / 2
	left, err := executeBulkWithBisect(items[:middle], execute, reject)
	if err != nil {
		return left, err
	}
	right, err := executeBulkWithBisect(items[middle:], execute, reject)
	return left + right, err
}
