package validator

const MaxOrderNumberLength = 256

func ValidOrderNumber(number string) bool {
	if number == "" || len(number) > MaxOrderNumberLength {
		return false
	}

	sum := 0
	double := len(number)%2 == 0

	for _, character := range number {
		if character < '0' || character > '9' {
			return false
		}

		digit := int(character - '0')
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}

		sum += digit
		double = !double
	}

	return sum%10 == 0
}
