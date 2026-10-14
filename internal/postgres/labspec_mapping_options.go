package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ktcloud4-SL/labbit-app/internal/server/repository"
)

func (q queries) ActiveImageMappingOptions(ctx context.Context, organizationID uuid.UUID) ([]repository.ImageMappingOption, error) {
	const op = "ActiveImageMappingOptions"
	rows, err := q.db.Query(ctx,
		`SELECT id, provider_connection_id, display_name
		 FROM provider_image_mappings
		 WHERE organization_id = $1 AND is_active
		 ORDER BY provider_connection_id, display_name, id`, organizationID)
	if err != nil {
		return nil, normalize(op, err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (repository.ImageMappingOption, error) {
		var item repository.ImageMappingOption
		err := row.Scan(&item.ID, &item.ProviderConnectionID, &item.DisplayName)
		return item, err
	})
	if err != nil {
		return nil, normalize(op, err)
	}
	return items, nil
}

func (q queries) ActiveFlavorMappingOptions(ctx context.Context, organizationID uuid.UUID) ([]repository.FlavorMappingOption, error) {
	const op = "ActiveFlavorMappingOptions"
	rows, err := q.db.Query(ctx,
		`SELECT id, provider_connection_id, display_name, vcpus, ram_mib, disk_gib
		 FROM provider_flavor_mappings
		 WHERE organization_id = $1 AND is_active
		 ORDER BY provider_connection_id, display_name, id`, organizationID)
	if err != nil {
		return nil, normalize(op, err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (repository.FlavorMappingOption, error) {
		var item repository.FlavorMappingOption
		err := row.Scan(&item.ID, &item.ProviderConnectionID, &item.DisplayName, &item.VCPUs, &item.RAMMiB, &item.DiskGiB)
		return item, err
	})
	if err != nil {
		return nil, normalize(op, err)
	}
	return items, nil
}
