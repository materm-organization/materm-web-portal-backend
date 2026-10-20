package formatter

func FullName(gender, firstName, lastName string) string {
	title := "ນາງ"
	if gender == "M" {
		title = "ທ້າວ"
	}
	return title + " " + firstName + " " + lastName
}
