package domain

type ProductRepository interface {
	GetByID(id string) (*Product, error)
	GetBySlug(slug string) (*Product, error)
	ListAll() ([]*Product, error)
	ListEnabled() ([]*Product, error)
	Save(product *Product) error
}
